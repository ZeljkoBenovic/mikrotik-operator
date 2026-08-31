package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestBackupSnapshotStoresExport(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", Generation: 1},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
	}
	rosClient := &recordingRouterClient{exportText: "/ip dns set servers=1.1.1.1\n"}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{Client: kube, Scheme: scheme, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("once")); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "once", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Export != rosClient.exportText {
		t.Fatalf("export = %q", stored.Status.Export)
	}
	if stored.Status.Role != api.BackupRoleSnapshot {
		t.Fatalf("role = %q", stored.Status.Role)
	}
	if !conditionReady(stored.Status.Conditions) {
		t.Fatalf("conditions = %#v", stored.Status.Conditions)
	}
}

func TestBackupSnapshotDoesNotReExportWhenUnchanged(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", Generation: 1},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status: api.MikroTikBackupStatus{
			Export:             "/ip dns\n",
			ObservedGeneration: 1,
			Role:               api.BackupRoleSnapshot,
		},
	}
	calls := 0
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			calls++
			return &recordingRouterClient{}, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("once")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("export was repeated %d times", calls)
	}
}

func TestBackupRejectsRemoteEnabled(t *testing.T) {
	scheme := backupTestScheme(t)
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app"},
		Spec: api.MikroTikBackupSpec{
			RouterRef: "edge",
			Remote:    &api.BackupRemoteSpec{Enabled: true},
		},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup).
		WithStatusSubresource(&api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{Client: kube, Scheme: scheme, Factory: backupFactory(&recordingRouterClient{})}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("once")); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "once", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if conditionReason(stored.Status.Conditions, "Ready") != api.ConditionRemoteNotImplemented {
		t.Fatalf("conditions = %#v", stored.Status.Conditions)
	}
	if conditionStatus(stored.Status.Conditions, api.ConditionRemoteNotImplemented) != metav1.ConditionTrue {
		t.Fatalf("missing RemoteStorageNotImplemented: %#v", stored.Status.Conditions)
	}
	firstReady := stored.Status.Conditions[0].LastTransitionTime
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("once")); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "once", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Conditions[0].LastTransitionTime.Equal(&firstReady) {
		t.Fatal("remote stub status was rewritten on every reconcile")
	}
}

func TestBackupPolicyCreatesSnapshotAndPrunes(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	retention := int32(1)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	policy := &api.MikroTikBackup{
		TypeMeta:   metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "MikroTikBackup"},
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "app", Generation: 1, UID: "policy-uid"},
		Spec: api.MikroTikBackupSpec{
			RouterRef: "edge",
			Schedule:  "0 * * * *",
			Retention: &retention,
		},
	}
	stale := &api.MikroTikBackup{
		TypeMeta: metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "MikroTikBackup"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "nightly-1000",
			Namespace:         "app",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Labels:            map[string]string{api.BackupPolicyLabel: "nightly"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: api.GroupVersion.String(),
				Kind:       "MikroTikBackup",
				Name:       "nightly",
				UID:        "policy-uid",
				Controller: boolPtr(true),
			}},
		},
		Spec: api.MikroTikBackupSpec{RouterRef: "edge"},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, policy, stale).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{
		Client:  kube,
		Scheme:  scheme,
		Factory: backupFactory(&recordingRouterClient{}),
		Now:     func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("nightly")); err != nil {
		t.Fatal(err)
	}
	var list api.MikroTikBackupList
	if err := kube.List(context.Background(), &list, client.InNamespace("app")); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, item := range list.Items {
		names[item.Name] = true
	}
	wantSnapshot := backupSnapshotName("nightly", now)
	if !names[wantSnapshot] {
		t.Fatalf("missing snapshot %s in %#v", wantSnapshot, names)
	}
	if names["nightly-1000"] {
		t.Fatal("stale snapshot was not pruned")
	}
	var stored api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "nightly", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Role != api.BackupRolePolicy {
		t.Fatalf("role = %q", stored.Status.Role)
	}
}

func TestRestoreWaitsForConfirmation(t *testing.T) {
	scheme := backupTestScheme(t)
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
		},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(restore).
		WithStatusSubresource(&api.MikroTikRestore{}).
		Build()
	calls := 0
	reconciler := RestoreReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			calls++
			return &recordingRouterClient{}, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("import ran before confirmation")
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if conditionReason(stored.Status.Conditions, "Ready") != "WaitingForConfirmation" {
		t.Fatalf("conditions = %#v", stored.Status.Conditions)
	}
}

func TestRestoreAppliesViaRouterRefOnce(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", UID: "backup-uid"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status:     api.MikroTikBackupStatus{Export: "/ip dns set servers=1.1.1.1\n"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
	}
	rosClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup, restore).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	reconciler := RestoreReconciler{Client: kube, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 1 || rosClient.imported[0] != backup.Status.Export {
		t.Fatalf("imported = %#v", rosClient.imported)
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 1 {
		t.Fatalf("successful restore was retried: %#v", rosClient.imported)
	}
}

func TestRestoreAppliesViaInlineConnection(t *testing.T) {
	scheme := backupTestScheme(t)
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app"},
		Status:     api.MikroTikBackupStatus{Export: "/ip address\n"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-router", Namespace: "app"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("secret")},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bare-metal", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			Confirm:   api.RestoreConfirmValue,
			Connection: &api.RestoreConnectionSpec{
				Address:           "192.0.2.88",
				CredentialsSecret: corev1.LocalObjectReference{Name: "empty-router"},
			},
		},
	}
	rosClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, secret, restore).
		WithStatusSubresource(&api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	var dialed string
	reconciler := RestoreReconciler{
		Client: kube,
		Factory: func(_ context.Context, address string, _ int32, _ bool, username, password string) (ros.Client, error) {
			dialed = address
			if username == "" || password == "" {
				t.Fatal("inline restore must read credentials from the Secret")
			}
			if strings.Contains(password, "logged") {
				t.Fatal("password leaked into test log path")
			}
			return rosClient, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bare-metal")); err != nil {
		t.Fatal(err)
	}
	if dialed != "192.0.2.88" {
		t.Fatalf("dialed %q", dialed)
	}
	if len(rosClient.imported) != 1 {
		t.Fatalf("imported = %#v", rosClient.imported)
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bare-metal", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Target != "192.0.2.88" || !stored.Status.Applied {
		t.Fatalf("status = %#v", stored.Status)
	}
}

func TestRestoreRejectsPolicyBackup(t *testing.T) {
	scheme := backupTestScheme(t)
	policy := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "app"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge", Schedule: "0 2 * * *"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "nightly"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(policy, restore).
		WithStatusSubresource(&api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	calls := 0
	reconciler := RestoreReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			calls++
			return &recordingRouterClient{}, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("import ran against a policy backup")
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if conditionReason(stored.Status.Conditions, "Ready") != "ApplyFailed" {
		t.Fatalf("conditions = %#v", stored.Status.Conditions)
	}
	if !strings.Contains(stored.Status.Conditions[0].Message, "schedule policy") {
		t.Fatalf("message = %q", stored.Status.Conditions[0].Message)
	}
}

func TestBackupPolicyKeepsCapturedSnapshotWhenNewCaptureFails(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	retention := int32(1)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	capturedAt := metav1.NewTime(now.Add(-2 * time.Hour))
	policy := &api.MikroTikBackup{
		TypeMeta:   metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "MikroTikBackup"},
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "app", Generation: 1, UID: "policy-uid"},
		Spec: api.MikroTikBackupSpec{
			RouterRef: "edge",
			Schedule:  "0 * * * *",
			Retention: &retention,
		},
	}
	stale := &api.MikroTikBackup{
		TypeMeta: metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "MikroTikBackup"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "nightly-1000",
			Namespace:         "app",
			CreationTimestamp: capturedAt,
			Labels:            map[string]string{api.BackupPolicyLabel: "nightly"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: api.GroupVersion.String(),
				Kind:       "MikroTikBackup",
				Name:       "nightly",
				UID:        "policy-uid",
				Controller: boolPtr(true),
			}},
		},
		Spec:   api.MikroTikBackupSpec{RouterRef: "edge"},
		Status: api.MikroTikBackupStatus{Export: "/ip dns\n", CapturedAt: &capturedAt},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, policy, stale).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{
		Client:  kube,
		Scheme:  scheme,
		Factory: backupFactory(&recordingRouterClient{}),
		Now:     func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("nightly")); err != nil {
		t.Fatal(err)
	}
	var storedStale api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "nightly-1000", Namespace: "app"}, &storedStale); err != nil {
		t.Fatalf("captured snapshot was pruned: %v", err)
	}
	var storedPolicy api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "nightly", Namespace: "app"}, &storedPolicy); err != nil {
		t.Fatal(err)
	}
	if storedPolicy.Status.LastScheduleTime != nil {
		t.Fatal("LastScheduleTime advanced before the due snapshot captured")
	}
	if conditionReady(storedPolicy.Status.Conditions) {
		t.Fatalf("policy should stay unready until capture, conditions=%#v", storedPolicy.Status.Conditions)
	}
}

func TestBackupPruneIgnoresUnownedLabelMatch(t *testing.T) {
	scheme := backupTestScheme(t)
	retention := int32(1)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	policy := &api.MikroTikBackup{
		TypeMeta:   metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "MikroTikBackup"},
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "app", UID: "policy-uid", Generation: 1},
		Spec: api.MikroTikBackupSpec{
			RouterRef: "edge",
			Schedule:  "0 * * * *",
			Retention: &retention,
		},
		Status: api.MikroTikBackupStatus{LastScheduleTime: &metav1.Time{Time: now.Add(time.Hour)}},
	}
	unowned := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "nightly-stolen",
			Namespace:         "app",
			CreationTimestamp: metav1.NewTime(now.Add(-time.Hour)),
			Labels:            map[string]string{api.BackupPolicyLabel: "nightly"},
		},
		Spec:   api.MikroTikBackupSpec{RouterRef: "edge"},
		Status: api.MikroTikBackupStatus{Export: "/ip dns\n"},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(policy, unowned).
		WithStatusSubresource(&api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{Client: kube, Scheme: scheme, Now: func() time.Time { return now }}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("nightly")); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "nightly-stolen", Namespace: "app"}, &stored); err != nil {
		t.Fatalf("unowned snapshot was deleted: %v", err)
	}
}

func TestBackupSnapshotClearsExportWhenOversized(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", Generation: 1},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status:     api.MikroTikBackupStatus{Export: "stale"},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{
		Client:  kube,
		Scheme:  scheme,
		Factory: backupFactory(&recordingRouterClient{exportText: strings.Repeat("x", api.MaxExportBytes+1)}),
	}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("once")); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "once", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Export != "" {
		t.Fatalf("oversized export was stored, bytes=%d", len(stored.Status.Export))
	}
	if conditionReady(stored.Status.Conditions) {
		t.Fatal("oversized export reported Ready")
	}
}

func TestBackupSnapshotReExportsOnGenerationBump(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", Generation: 2},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status: api.MikroTikBackupStatus{
			Export:             "/old\n",
			ObservedGeneration: 1,
		},
	}
	rosClient := &recordingRouterClient{exportText: "/new\n"}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}).
		Build()
	reconciler := BackupReconciler{Client: kube, Scheme: scheme, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), backupRequest("once")); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikBackup
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "once", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Export != "/new\n" {
		t.Fatalf("generation bump did not re-export: %q", stored.Status.Export)
	}
}

func TestRestoreRejectsEmptyExport(t *testing.T) {
	scheme := backupTestScheme(t)
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup, restore).
		WithStatusSubresource(&api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	calls := 0
	reconciler := RestoreReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			calls++
			return &recordingRouterClient{}, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("import ran against empty export")
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Status.Conditions[0].Message, "no stored /export") {
		t.Fatalf("message = %q", stored.Status.Conditions[0].Message)
	}
}

func TestRestoreIgnoresConfirmTrue(t *testing.T) {
	scheme := backupTestScheme(t)
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   "true",
		},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(restore).
		WithStatusSubresource(&api.MikroTikRestore{}).
		Build()
	calls := 0
	reconciler := RestoreReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			calls++
			return &recordingRouterClient{}, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("import ran for confirm=true")
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if conditionReason(stored.Status.Conditions, "Ready") != "WaitingForConfirmation" {
		t.Fatalf("conditions = %#v", stored.Status.Conditions)
	}
}

func TestRestoreGenerationBumpDoesNotReimport(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", UID: "backup-uid"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status:     api.MikroTikBackupStatus{Export: "/ip dns\n"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 2},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
		Status: api.MikroTikRestoreStatus{
			Applied:            true,
			BackupUID:          "backup-uid",
			Target:             "edge",
			ObservedGeneration: 1,
		},
	}
	rosClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup, restore).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	reconciler := RestoreReconciler{Client: kube, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 0 {
		t.Fatalf("generation bump re-imported: %#v", rosClient.imported)
	}
}

func TestRestoreStatusFailureAfterImportDoesNotReimport(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", UID: "backup-uid"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status:     api.MikroTikBackupStatus{Export: "/ip dns\n"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
	}
	rosClient := &recordingRouterClient{}
	appliedUpdates := 0
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup, restore).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	kube := interceptor.NewClient(base, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, underlying client.Client, subresource string, object client.Object, options ...client.SubResourceUpdateOption) error {
			current, ok := object.(*api.MikroTikRestore)
			if ok && current.Status.Applied {
				appliedUpdates++
				if appliedUpdates == 1 {
					return fmt.Errorf("injected status failure")
				}
			}
			return underlying.SubResource(subresource).Update(ctx, object, options...)
		},
	})
	reconciler := RestoreReconciler{Client: kube, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err == nil {
		t.Fatal("expected injected status failure")
	}
	if len(rosClient.imported) != 1 {
		t.Fatalf("imported = %#v", rosClient.imported)
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 1 {
		t.Fatalf("import ran again after status failure: %#v", rosClient.imported)
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied {
		t.Fatalf("status = %#v", stored.Status)
	}
}

func TestRestoreImportInProgressWithoutCompletionRetriesImport(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", UID: "backup-uid"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status:     api.MikroTikBackupStatus{Export: "/ip dns\n"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
		Status: api.MikroTikRestoreStatus{
			Applied:            false,
			BackupUID:          "backup-uid",
			Target:             "edge",
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  api.ConditionImportInProgress,
				Message: "RouterOS /import is in progress",
			}},
		},
	}
	rosClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup, restore).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	reconciler := RestoreReconciler{Client: kube, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 1 {
		t.Fatalf("ImportInProgress skipped /import: %#v", rosClient.imported)
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied {
		t.Fatalf("status = %#v", stored.Status)
	}
}

func TestRestoreAppliedStaysAppliedWhenConfirmCleared(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", UID: "backup-uid"},
		Spec:       api.MikroTikBackupSpec{RouterRef: "edge"},
		Status:     api.MikroTikBackupStatus{Export: "/ip dns\n"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 2},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
		},
		Status: api.MikroTikRestoreStatus{
			Applied:            true,
			BackupUID:          "backup-uid",
			Target:             "edge",
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type:   "Ready",
				Status: metav1.ConditionTrue,
				Reason: "Applied",
			}},
		},
	}
	rosClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup, restore).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	reconciler := RestoreReconciler{Client: kube, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 0 {
		t.Fatalf("cleared confirm re-imported: %#v", rosClient.imported)
	}
	var stored api.MikroTikRestore
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "bring-up", Namespace: "app"}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied {
		t.Fatalf("cleared confirm wiped applied status: %#v", stored.Status)
	}
	if conditionReason(stored.Status.Conditions, "Ready") == "WaitingForConfirmation" {
		t.Fatalf("applied restore was unconfirmed: %#v", stored.Status.Conditions)
	}
	stored.Spec.Confirm = api.RestoreConfirmValue
	if err := kube.Update(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 0 {
		t.Fatalf("re-setting RESTORE re-imported: %#v", rosClient.imported)
	}
}

func TestRestoreUsesInactiveRouterRef(t *testing.T) {
	scheme := backupTestScheme(t)
	router, secret := backupTestRouter()
	router.Finalizers = nil
	router.Status.AppliedEndpoints = nil
	backup := &api.MikroTikBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "once", Namespace: "app", UID: "backup-uid"},
		Status:     api.MikroTikBackupStatus{Export: "/ip dns\n"},
	}
	restore := &api.MikroTikRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "bring-up", Namespace: "app", Generation: 1},
		Spec: api.MikroTikRestoreSpec{
			BackupRef: api.NamespacedName{Name: "once"},
			RouterRef: "edge",
			Confirm:   api.RestoreConfirmValue,
		},
	}
	rosClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(router, secret, backup, restore).
		WithStatusSubresource(&api.MikroTikRouter{}, &api.MikroTikBackup{}, &api.MikroTikRestore{}).
		Build()
	reconciler := RestoreReconciler{Client: kube, Factory: backupFactory(rosClient)}
	if _, err := reconciler.Reconcile(context.Background(), restoreRequest("bring-up")); err != nil {
		t.Fatal(err)
	}
	if len(rosClient.imported) != 1 {
		t.Fatalf("inactive routerRef restore did not import: %#v", rosClient.imported)
	}
}

func TestValidateRestoreSpec(t *testing.T) {
	t.Parallel()
	err := validateRestoreSpec(api.MikroTikRestoreSpec{
		BackupRef: api.NamespacedName{Name: "once"},
		RouterRef: "edge",
		Connection: &api.RestoreConnectionSpec{
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	})
	if err == nil {
		t.Fatal("expected xor error")
	}
	if err := validateRestoreSpec(api.MikroTikRestoreSpec{RouterRef: "edge"}); err == nil {
		t.Fatal("expected backupRef error")
	}
}

func backupTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func backupTestRouter() (*api.MikroTikRouter, *corev1.Secret) {
	endpoint := api.RouterEndpoint{
		Name:              "edge",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
	}
	router := &api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("pw")},
	}
	return router, secret
}

func backupFactory(rosClient ros.Client) ros.Factory {
	return func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return rosClient, nil
	}
}

func backupRequest(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "app"}}
}

func restoreRequest(name string) reconcile.Request {
	return backupRequest(name)
}

func boolPtr(value bool) *bool { return &value }

func conditionReady(conditions []metav1.Condition) bool {
	return conditionStatus(conditions, "Ready") == metav1.ConditionTrue
}

func conditionStatus(conditions []metav1.Condition, typeName string) metav1.ConditionStatus {
	for _, condition := range conditions {
		if condition.Type == typeName {
			return condition.Status
		}
	}
	return ""
}

func conditionReason(conditions []metav1.Condition, typeName string) string {
	for _, condition := range conditions {
		if condition.Type == typeName {
			return condition.Reason
		}
	}
	return ""
}
