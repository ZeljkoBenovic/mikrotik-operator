package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type BackupReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Factory ros.Factory
	Now     func() time.Time
}

func (r *BackupReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *BackupReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var backup api.MikroTikBackup
	if err := r.Get(ctx, req.NamespacedName, &backup); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !backup.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}
	if strings.TrimSpace(backup.Spec.RouterRef) == "" {
		return r.failBackup(ctx, &backup, fmt.Errorf("routerRef is required"))
	}
	if backup.Spec.RemoteEnabled() {
		return r.failBackupRemote(ctx, &backup)
	}
	if backup.Spec.IsPolicy() {
		return r.reconcilePolicy(ctx, &backup)
	}
	return r.reconcileSnapshot(ctx, &backup)
}

func (r *BackupReconciler) reconcilePolicy(ctx context.Context, backup *api.MikroTikBackup) (reconcile.Result, error) {
	now := r.now()
	from := now
	if backup.Status.LastScheduleTime != nil {
		from = backup.Status.LastScheduleTime.Time
	}
	next, err := nextBackupTime(backup.Spec.Schedule, from)
	if err != nil {
		return r.failBackup(ctx, backup, err)
	}
	due := backup.Status.LastScheduleTime == nil || !now.Before(next)
	if due {
		tick := r.policyTick(backup, now, next)
		if err := r.createSnapshot(ctx, backup, tick); err != nil {
			return r.failBackup(ctx, backup, err)
		}
		if err := r.pruneSnapshots(ctx, backup); err != nil {
			return r.failBackup(ctx, backup, err)
		}
		owned, err := r.listOwnedSnapshots(ctx, backup)
		if err != nil {
			return reconcile.Result{}, err
		}
		name := backupSnapshotName(backup.Name, tick)
		if !snapshotHasCapture(owned, name) {
			return r.policyStatus(ctx, backup, time.Time{}, next, owned)
		}
		next, err = nextBackupTime(backup.Spec.Schedule, now)
		if err != nil {
			return r.failBackup(ctx, backup, err)
		}
		return r.policyStatus(ctx, backup, now, next, owned)
	}
	if err := r.pruneSnapshots(ctx, backup); err != nil {
		return r.failBackup(ctx, backup, err)
	}
	owned, err := r.listOwnedSnapshots(ctx, backup)
	if err != nil {
		return reconcile.Result{}, err
	}
	return r.policyStatus(ctx, backup, time.Time{}, next, owned)
}

func (r *BackupReconciler) policyTick(policy *api.MikroTikBackup, now, next time.Time) time.Time {
	if policy.Status.LastScheduleTime != nil {
		return next
	}
	if !policy.CreationTimestamp.IsZero() {
		return policy.CreationTimestamp.Time
	}
	return now
}

func (r *BackupReconciler) createSnapshot(ctx context.Context, policy *api.MikroTikBackup, at time.Time) error {
	routerKey, err := resolveRouterReference(ctx, r.Client, policy.Namespace, policy.Spec.RouterRef)
	if err != nil {
		return err
	}
	name := backupSnapshotName(policy.Name, at)
	var existing api.MikroTikBackup
	err = r.Get(ctx, types.NamespacedName{Name: name, Namespace: policy.Namespace}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	snapshot := &api.MikroTikBackup{
		TypeMeta: metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "MikroTikBackup"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: policy.Namespace,
			Labels: map[string]string{
				api.BackupPolicyLabel:          policy.Name,
				api.BackupRouterNameLabel:      routerKey.Name,
				api.BackupRouterNamespaceLabel: routerKey.Namespace,
			},
		},
		Spec: api.MikroTikBackupSpec{
			RouterRef: policy.Spec.RouterRef,
		},
	}
	policy.SetGroupVersionKind(api.GroupVersion.WithKind("MikroTikBackup"))
	if err := controllerutil.SetControllerReference(policy, snapshot, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, snapshot)
}

func (r *BackupReconciler) pruneSnapshots(ctx context.Context, policy *api.MikroTikBackup) error {
	owned, err := r.listOwnedSnapshots(ctx, policy)
	if err != nil {
		return err
	}
	for _, stale := range snapshotsToDelete(owned, policy.Spec.RetentionCount()) {
		snapshot := stale
		if err := r.Delete(ctx, &snapshot); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *BackupReconciler) listOwnedSnapshots(ctx context.Context, policy *api.MikroTikBackup) ([]api.MikroTikBackup, error) {
	var list api.MikroTikBackupList
	if err := r.List(ctx, &list, client.InNamespace(policy.Namespace), client.MatchingLabels{
		api.BackupPolicyLabel: policy.Name,
	}); err != nil {
		return nil, err
	}
	owned := make([]api.MikroTikBackup, 0, len(list.Items))
	for _, item := range list.Items {
		if !metav1.IsControlledBy(&item, policy) {
			continue
		}
		owned = append(owned, item)
	}
	return owned, nil
}

func (r *BackupReconciler) reconcileSnapshot(ctx context.Context, backup *api.MikroTikBackup) (reconcile.Result, error) {
	if backup.Status.Export != "" && backup.Status.ObservedGeneration == backup.Generation {
		return reconcile.Result{}, nil
	}
	routerKey, err := resolveRouterReference(ctx, r.Client, backup.Namespace, backup.Spec.RouterRef)
	if err != nil {
		return r.failBackup(ctx, backup, err)
	}
	var export string
	err = withRouterConnections(ctx, r.Client, r.Factory, routerKey, true, func(_ api.MikroTikRouter, connections []routerConnection) error {
		if len(connections) == 0 {
			return fmt.Errorf("no RouterOS connections for %s/%s", routerKey.Namespace, routerKey.Name)
		}
		text, err := connections[0].Client.Export(ctx)
		if err != nil {
			return err
		}
		export = text
		return nil
	})
	if err != nil {
		return r.failBackup(ctx, backup, err)
	}
	if len(export) > api.MaxExportBytes {
		backup.Status.Export = ""
		backup.Status.ExportBytes = 0
		backup.Status.CapturedAt = nil
		backup.Status.ExportWarning = exportSizeWarning(len(export))
		return r.failBackup(ctx, backup, fmt.Errorf("%s", exportSizeWarning(len(export))))
	}
	captured := metav1.NewTime(r.now())
	oldStatus := backup.Status
	backup.Status.Role = api.BackupRoleSnapshot
	backup.Status.RouterRef = formatRouterRef(backup.Namespace, routerKey)
	backup.Status.Export = export
	backup.Status.ExportBytes = int64(len(export))
	backup.Status.ExportWarning = exportSizeWarning(len(export))
	backup.Status.CapturedAt = &captured
	backup.Status.ObservedGeneration = backup.Generation
	backup.Status.Conditions = readyCondition(backup.Status.Conditions, metav1.ConditionTrue, "Captured", "RouterOS /export stored")
	if reflect.DeepEqual(oldStatus, backup.Status) {
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, r.Status().Update(ctx, backup)
}

func (r *BackupReconciler) policyStatus(ctx context.Context, backup *api.MikroTikBackup, last time.Time, next time.Time, owned []api.MikroTikBackup) (reconcile.Result, error) {
	oldStatus := backup.Status
	backup.Status.Role = api.BackupRolePolicy
	backup.Status.RouterRef = backup.Spec.RouterRef
	backup.Status.SnapshotCount = int32(len(owned))
	backup.Status.ObservedGeneration = backup.Generation
	if !last.IsZero() {
		stamp := metav1.NewTime(last)
		backup.Status.LastScheduleTime = &stamp
	}
	if !next.IsZero() {
		stamp := metav1.NewTime(next)
		backup.Status.NextScheduleTime = &stamp
	}
	ready, reason, message := policyReady(owned)
	backup.Status.Conditions = readyCondition(backup.Status.Conditions, ready, reason, message)
	requeueAfter := next.Sub(r.now())
	if ready != metav1.ConditionTrue {
		requeueAfter = time.Second
	}
	if requeueAfter < time.Second {
		requeueAfter = time.Second
	}
	if reflect.DeepEqual(oldStatus, backup.Status) {
		return reconcile.Result{RequeueAfter: requeueAfter}, nil
	}
	return reconcile.Result{RequeueAfter: requeueAfter}, r.Status().Update(ctx, backup)
}

func policyReady(owned []api.MikroTikBackup) (metav1.ConditionStatus, string, string) {
	latest := latestOwnedSnapshot(owned)
	if latest == nil {
		return metav1.ConditionFalse, "SnapshotPending", "waiting for the first snapshot to capture"
	}
	if !snapshotHasExport(*latest) {
		return metav1.ConditionFalse, "SnapshotPending", "waiting for snapshot " + latest.Name + " to capture"
	}
	if conditionStatusOf(latest.Status.Conditions, "Ready") == metav1.ConditionFalse {
		reason := conditionReasonOf(latest.Status.Conditions, "Ready")
		if reason == "" {
			reason = "SnapshotFailed"
		}
		message := conditionMessageOf(latest.Status.Conditions, "Ready")
		if message == "" {
			message = "latest snapshot " + latest.Name + " is not Ready"
		}
		return metav1.ConditionFalse, reason, message
	}
	return metav1.ConditionTrue, "Scheduled", "backup schedule is active"
}

func (r *BackupReconciler) failBackup(ctx context.Context, backup *api.MikroTikBackup, err error) (reconcile.Result, error) {
	oldStatus := backup.Status
	role := api.BackupRoleSnapshot
	if backup.Spec.IsPolicy() {
		role = api.BackupRolePolicy
	}
	backup.Status.Role = role
	backup.Status.Conditions = readyCondition(backup.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", err.Error())
	if reflect.DeepEqual(oldStatus, backup.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, r.Status().Update(ctx, backup)
}

func (r *BackupReconciler) failBackupRemote(ctx context.Context, backup *api.MikroTikBackup) (reconcile.Result, error) {
	oldStatus := backup.Status
	role := api.BackupRoleSnapshot
	if backup.Spec.IsPolicy() {
		role = api.BackupRolePolicy
	}
	backup.Status.Role = role
	backup.Status.Conditions = remoteNotImplementedConditions(backup.Status.Conditions)
	if reflect.DeepEqual(oldStatus, backup.Status) {
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, r.Status().Update(ctx, backup)
}

func remoteNotImplementedConditions(existing []metav1.Condition) []metav1.Condition {
	readyMsg := "remote backup storage is not implemented; keep spec.remote.enabled false"
	remoteMsg := "FTP, SMB, and S3 backup storage are reserved for a future release"
	now := metav1.Now()
	ready := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             api.ConditionRemoteNotImplemented,
		Message:            readyMsg,
		LastTransitionTime: now,
	}
	remote := metav1.Condition{
		Type:               api.ConditionRemoteNotImplemented,
		Status:             metav1.ConditionTrue,
		Reason:             api.ConditionRemoteNotImplemented,
		Message:            remoteMsg,
		LastTransitionTime: now,
	}
	for _, condition := range existing {
		if condition.Type == ready.Type && condition.Status == ready.Status && condition.Reason == ready.Reason && condition.Message == ready.Message {
			ready.LastTransitionTime = condition.LastTransitionTime
		}
		if condition.Type == remote.Type && condition.Status == remote.Status && condition.Reason == remote.Reason && condition.Message == remote.Message {
			remote.LastTransitionTime = condition.LastTransitionTime
		}
	}
	return []metav1.Condition{ready, remote}
}

func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.MikroTikBackup{}).
		Owns(&api.MikroTikBackup{}).
		Complete(r)
}

func formatRouterRef(resourceNamespace string, key types.NamespacedName) string {
	if key.Namespace == "" || key.Namespace == resourceNamespace {
		return key.Name
	}
	return key.Namespace + "/" + key.Name
}

type RestoreReconciler struct {
	client.Client
	Factory ros.Factory
	Now     func() time.Time
}

func (r *RestoreReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *RestoreReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var restore api.MikroTikRestore
	if err := r.Get(ctx, req.NamespacedName, &restore); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !restore.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}
	if err := validateRestoreSpec(restore.Spec); err != nil {
		return r.failRestore(ctx, &restore, err)
	}
	if !restore.Spec.Confirmed() && !restore.Status.Applied {
		return r.waitForConfirm(ctx, &restore)
	}
	backup, err := r.loadBackup(ctx, restore)
	if err != nil {
		return r.failRestore(ctx, &restore, err)
	}
	if backup.Spec.IsPolicy() {
		return r.failRestore(ctx, &restore, fmt.Errorf("backup %s/%s is a schedule policy; set backupRef to a snapshot child that has status.export", backup.Namespace, backup.Name))
	}
	if strings.TrimSpace(backup.Status.Export) == "" {
		return r.failRestore(ctx, &restore, fmt.Errorf("backup %s/%s has no stored /export", backup.Namespace, backup.Name))
	}
	target, err := r.restoreTarget(ctx, restore)
	if err != nil {
		return r.failRestore(ctx, &restore, err)
	}
	if restoreAlreadyApplied(restore, backup, target) {
		if restore.Status.ObservedGeneration == restore.Generation {
			return reconcile.Result{}, nil
		}
		oldStatus := restore.Status
		restore.Status.ObservedGeneration = restore.Generation
		if reflect.DeepEqual(oldStatus, restore.Status) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, r.Status().Update(ctx, &restore)
	}
	if !restore.Spec.Confirmed() {
		return r.waitForConfirm(ctx, &restore)
	}
	if restoreImportCompleted(restore, backup, target) {
		return r.finishRestore(ctx, &restore, backup, target)
	}
	if err := r.markImportInProgress(ctx, &restore, backup, target); err != nil {
		return reconcile.Result{}, err
	}
	if _, err := r.applyRestore(ctx, restore, backup.Status.Export); err != nil {
		return r.failRestore(ctx, &restore, err)
	}
	if err := r.markImportSucceeded(ctx, &restore, backup, target); err != nil {
		return reconcile.Result{}, err
	}
	return r.finishRestore(ctx, &restore, backup, target)
}

func restoreAlreadyApplied(restore api.MikroTikRestore, backup *api.MikroTikBackup, target string) bool {
	return restore.Status.Applied &&
		restore.Status.BackupUID == string(backup.UID) &&
		restore.Status.Target == target
}

func restoreImportCompleted(restore api.MikroTikRestore, backup *api.MikroTikBackup, target string) bool {
	return restore.Status.BackupUID == string(backup.UID) &&
		restore.Status.Target == target &&
		conditionReasonOf(restore.Status.Conditions, "Ready") == api.ConditionImportSucceeded
}

func (r *RestoreReconciler) markImportInProgress(ctx context.Context, restore *api.MikroTikRestore, backup *api.MikroTikBackup, target string) error {
	oldStatus := restore.Status
	restore.Status.Applied = false
	restore.Status.BackupUID = string(backup.UID)
	restore.Status.Target = target
	restore.Status.ObservedGeneration = restore.Generation
	restore.Status.Conditions = readyCondition(
		restore.Status.Conditions,
		metav1.ConditionFalse,
		api.ConditionImportInProgress,
		"RouterOS /import is in progress",
	)
	if reflect.DeepEqual(oldStatus, restore.Status) {
		return nil
	}
	return r.Status().Update(ctx, restore)
}

func (r *RestoreReconciler) markImportSucceeded(ctx context.Context, restore *api.MikroTikRestore, backup *api.MikroTikBackup, target string) error {
	oldStatus := restore.Status
	restore.Status.Applied = false
	restore.Status.BackupUID = string(backup.UID)
	restore.Status.Target = target
	restore.Status.ObservedGeneration = restore.Generation
	restore.Status.Conditions = readyCondition(
		restore.Status.Conditions,
		metav1.ConditionFalse,
		api.ConditionImportSucceeded,
		"RouterOS /import succeeded",
	)
	if reflect.DeepEqual(oldStatus, restore.Status) {
		return nil
	}
	return r.Status().Update(ctx, restore)
}

func (r *RestoreReconciler) finishRestore(ctx context.Context, restore *api.MikroTikRestore, backup *api.MikroTikBackup, target string) (reconcile.Result, error) {
	appliedAt := restore.Status.AppliedAt
	if appliedAt == nil || appliedAt.IsZero() {
		stamp := metav1.NewTime(r.now())
		appliedAt = &stamp
	}
	oldStatus := restore.Status
	restore.Status.Applied = true
	restore.Status.AppliedAt = appliedAt
	restore.Status.BackupUID = string(backup.UID)
	restore.Status.Target = target
	restore.Status.ObservedGeneration = restore.Generation
	restore.Status.Conditions = readyCondition(restore.Status.Conditions, metav1.ConditionTrue, "Applied", "RouterOS /import completed")
	if reflect.DeepEqual(oldStatus, restore.Status) {
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, r.Status().Update(ctx, restore)
}

func validateRestoreSpec(spec api.MikroTikRestoreSpec) error {
	if strings.TrimSpace(spec.BackupRef.Name) == "" {
		return fmt.Errorf("backupRef.name is required")
	}
	hasRouter := strings.TrimSpace(spec.RouterRef) != ""
	hasConn := spec.UsesInlineConnection()
	if hasRouter == hasConn {
		return fmt.Errorf("spec must set exactly one of routerRef or connection")
	}
	return nil
}

func (r *RestoreReconciler) loadBackup(ctx context.Context, restore api.MikroTikRestore) (*api.MikroTikBackup, error) {
	namespace := restore.Spec.BackupRef.Namespace
	if namespace == "" {
		namespace = restore.Namespace
	}
	var backup api.MikroTikBackup
	if err := r.Get(ctx, types.NamespacedName{Name: restore.Spec.BackupRef.Name, Namespace: namespace}, &backup); err != nil {
		return nil, err
	}
	return &backup, nil
}

func (r *RestoreReconciler) restoreTarget(ctx context.Context, restore api.MikroTikRestore) (string, error) {
	if restore.Spec.UsesInlineConnection() {
		return restore.Spec.Connection.Address, nil
	}
	routerKey, err := resolveRouterReference(ctx, r.Client, restore.Namespace, restore.Spec.RouterRef)
	if err != nil {
		return "", err
	}
	return formatRouterRef(restore.Namespace, routerKey), nil
}

func (r *RestoreReconciler) applyRestore(ctx context.Context, restore api.MikroTikRestore, script string) (string, error) {
	if restore.Spec.UsesInlineConnection() {
		return r.importInline(ctx, restore, script)
	}
	routerKey, err := resolveRouterReference(ctx, r.Client, restore.Namespace, restore.Spec.RouterRef)
	if err != nil {
		return "", err
	}
	err = withRouterConnections(ctx, r.Client, r.Factory, routerKey, false, func(_ api.MikroTikRouter, connections []routerConnection) error {
		if len(connections) == 0 {
			return fmt.Errorf("no RouterOS connections for %s/%s", routerKey.Namespace, routerKey.Name)
		}
		return connections[0].Client.Import(ctx, script)
	})
	if err != nil {
		return "", err
	}
	return formatRouterRef(restore.Namespace, routerKey), nil
}

func (r *RestoreReconciler) importInline(ctx context.Context, restore api.MikroTikRestore, script string) (string, error) {
	conn := restore.Spec.Connection
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: conn.CredentialsSecret.Name, Namespace: restore.Namespace}, &secret); err != nil {
		return "", err
	}
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	fenceKey, err := r.inlineRestoreFence(ctx, conn.Address)
	if err != nil {
		return "", err
	}
	var target string
	err = routerOperationFences.withFence(ctx, fenceKey, func() (operationErr error) {
		client, err := r.Factory(ctx, conn.Address, conn.Port, conn.TLS, username, password)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := client.Close(); closeErr != nil {
				operationErr = errors.Join(operationErr, fmt.Errorf("close RouterOS client: %w", closeErr))
			}
		}()
		if operationErr = client.Import(ctx, script); operationErr != nil {
			return operationErr
		}
		target = conn.Address
		return nil
	})
	return target, err
}

func (r *RestoreReconciler) inlineRestoreFence(ctx context.Context, address string) (types.NamespacedName, error) {
	var routers api.MikroTikRouterList
	if err := r.List(ctx, &routers); err != nil {
		return types.NamespacedName{}, err
	}
	matches := make([]types.NamespacedName, 0)
	for _, router := range routers.Items {
		if !router.DeletionTimestamp.IsZero() {
			continue
		}
		for _, endpoint := range routerEndpoints(router) {
			if endpoint.Address != address {
				continue
			}
			matches = append(matches, types.NamespacedName{Namespace: router.Namespace, Name: router.Name})
			break
		}
	}
	if len(matches) == 0 {
		return types.NamespacedName{Namespace: "restore-inline", Name: strings.ReplaceAll(address, ":", "-")}, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Namespace == matches[j].Namespace {
			return matches[i].Name < matches[j].Name
		}
		return matches[i].Namespace < matches[j].Namespace
	})
	return matches[0], nil
}

func (r *RestoreReconciler) waitForConfirm(ctx context.Context, restore *api.MikroTikRestore) (reconcile.Result, error) {
	oldStatus := restore.Status
	restore.Status.Applied = false
	restore.Status.Conditions = readyCondition(
		restore.Status.Conditions,
		metav1.ConditionFalse,
		"WaitingForConfirmation",
		`set spec.confirm to "RESTORE" to run /import of the stored export; this does not wipe the device`,
	)
	if reflect.DeepEqual(oldStatus, restore.Status) {
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, r.Status().Update(ctx, restore)
}

func (r *RestoreReconciler) failRestore(ctx context.Context, restore *api.MikroTikRestore, err error) (reconcile.Result, error) {
	oldStatus := restore.Status
	restore.Status.Applied = false
	restore.Status.Conditions = readyCondition(restore.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", err.Error())
	if reflect.DeepEqual(oldStatus, restore.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, r.Status().Update(ctx, restore)
}

func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.MikroTikRestore{}).
		Complete(r)
}
