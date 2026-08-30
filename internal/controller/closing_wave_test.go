package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestRouterActiveGateUsesPhysicalEndpointHistory(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	current := api.RouterEndpoint{
		Address:           "router.example.com",
		CredentialsSecret: corev1.LocalObjectReference{Name: "rotated-credentials"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "router",
			Namespace:  "app",
			Finalizers: []string{resourceFinalizer},
		},
		Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{current}},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{{
			Address:           "ROUTER.EXAMPLE.COM.",
			Port:              8728,
			CredentialsSecret: corev1.LocalObjectReference{Name: "old-credentials"},
		}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	if err := ensureRouterActive(context.Background(), kube, router); err != nil {
		t.Fatalf("credential rotation on the same physical endpoint was blocked: %v", err)
	}

	router.Status.AppliedEndpoints = append(router.Status.AppliedEndpoints, api.RouterEndpoint{
		Address:           "192.0.2.25",
		CredentialsSecret: corev1.LocalObjectReference{Name: "old-credentials"},
	})
	if err := ensureRouterActive(context.Background(), kube, router); err == nil {
		t.Fatal("obsolete physical endpoint history did not block child writes")
	}
}

func TestInvalidRouterDoesNotAcquireFinalizerOrDial(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "app"}}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router).
		WithStatusSubresource(&api.MikroTikRouter{}).
		Build()
	dials := 0
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return nil, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "empty")); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikRouter
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "app", Name: "empty"}, &stored); err != nil {
		t.Fatal(err)
	}
	if controllerutil.ContainsFinalizer(&stored, resourceFinalizer) {
		t.Fatal("invalid Router acquired an external-cleanup finalizer")
	}
	if dials != 0 {
		t.Fatalf("invalid Router dialed RouterOS %d times", dials)
	}
}

func TestInvalidFinalizedRouterCanDeleteWithoutDial(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := metav1.NewTime(time.Now())
	router := api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{
		Name:              "empty",
		Namespace:         "app",
		Finalizers:        []string{resourceFinalizer},
		DeletionTimestamp: &now,
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	dials := 0
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return nil, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "empty")); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("deleting invalid Router dialed RouterOS %d times", dials)
	}
}

func TestGeneratedDNSClaimIsUniqueEvenForSameTarget(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	winner := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "app", UID: types.UID("winner")}}
	loser := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "app", UID: types.UID("loser")}}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "app"},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: "router",
			Name:      "shared.example.com",
			Address:   "10.0.0.10",
			ServiceRef: &api.NamespacedName{
				Namespace: "app",
				Name:      "backend",
			},
		},
	}
	if err := controllerutil.SetControllerReference(&winner, &record, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&winner, &loser, &record).Build()
	_, err := preflightGeneratedChildClaims(
		context.Background(),
		kube,
		&loser,
		"router",
		"",
		[]generatedDNSCandidate{{
			childName: "desired",
			hostname:  "SHARED.EXAMPLE.COM.",
			service:   types.NamespacedName{Namespace: "app", Name: "backend"},
			address:   "10.0.0.10",
		}},
		nil,
	)
	if !errors.Is(err, errGeneratedChildCollision) {
		t.Fatalf("same-target hostname claim was not rejected deterministically: %v", err)
	}
}

func TestGeneratedDNSClaimPriorityConvergesWhenBothOwnersExist(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	winner := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "app", UID: types.UID("winner")}}
	loser := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "app", UID: types.UID("loser")}}
	winnerRecord := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "winner-record", Namespace: "app"},
		Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router", Name: "shared.example.com", Address: "10.0.0.10"},
	}
	loserRecord := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "loser-record", Namespace: "app"},
		Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router", Name: "shared.example.com", Address: "10.0.0.20"},
	}
	if err := controllerutil.SetControllerReference(&winner, &winnerRecord, scheme); err != nil {
		t.Fatal(err)
	}
	if err := controllerutil.SetControllerReference(&loser, &loserRecord, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&winner, &loser, &winnerRecord, &loserRecord).Build()
	candidate := []generatedDNSCandidate{{childName: "desired", hostname: "shared.example.com", address: "10.0.0.10"}}
	if _, err := preflightGeneratedChildClaims(context.Background(), kube, &winner, "router", "", candidate, nil); !errors.Is(err, errGeneratedClaimWaiting) {
		t.Fatalf("winning owner did not preserve its claim while the loser still existed: %v", err)
	}
	if _, err := preflightGeneratedChildClaims(context.Background(), kube, &loser, "router", "", candidate, nil); !errors.Is(err, errGeneratedChildCollision) {
		t.Fatalf("losing owner did not yield its claim: %v", err)
	}
}

func TestDirectResourcesTakeCanonicalClaimPriority(t *testing.T) {
	t.Run("DNS", func(t *testing.T) {
		scheme := controllerTestScheme(t)
		parent := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "generated-owner", Namespace: "app", UID: "generated-owner-uid"}}
		generated := api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "app"},
			Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router-a", Name: "shared.example.com", Address: "10.0.0.10"},
		}
		if err := controllerutil.SetControllerReference(&parent, &generated, scheme); err != nil {
			t.Fatal(err)
		}
		direct := api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "app", UID: "direct-uid"},
			Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router-a", Name: "shared.example.com", Address: "10.0.0.20"},
		}
		differentRouter := api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "different-router", Namespace: "app", UID: "different-uid"},
			Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router-b", Name: "shared.example.com", Address: "10.0.0.30"},
		}
		kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&parent, &generated, &direct, &differentRouter).Build()
		directCandidate := []generatedDNSCandidate{{hostname: direct.Spec.Name, address: direct.Spec.Address}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &direct, direct.Spec.RouterRef, "", directCandidate, nil); !errors.Is(err, errGeneratedClaimWaiting) {
			t.Fatalf("generated-first/direct-second did not select the direct DNS claim: %v", err)
		}
		generatedCandidate := []generatedDNSCandidate{{childName: generated.Name, hostname: generated.Spec.Name, address: generated.Spec.Address}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &parent, generated.Spec.RouterRef, "", generatedCandidate, nil); !errors.Is(err, errGeneratedChildCollision) {
			t.Fatalf("direct-first/generated-second did not reject the generated DNS claim: %v", err)
		}
		differentCandidate := []generatedDNSCandidate{{hostname: differentRouter.Spec.Name, address: differentRouter.Spec.Address}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &differentRouter, differentRouter.Spec.RouterRef, "", differentCandidate, nil); err != nil {
			t.Fatalf("same hostname on a different Router was rejected: %v", err)
		}
	})

	t.Run("port forward", func(t *testing.T) {
		scheme := controllerTestScheme(t)
		parent := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "generated-owner", Namespace: "app", UID: "generated-owner-uid"}}
		generated := api.MikroTikPortForward{
			ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: "app", Annotations: map[string]string{api.PublicIPAnnotation: "198.51.100.10"}},
			Spec:       api.MikroTikPortForwardSpec{RouterRef: "router-a", Protocol: "tcp", ExternalPort: 443, TargetAddress: "10.0.0.10", TargetPort: 8443},
		}
		if err := controllerutil.SetControllerReference(&parent, &generated, scheme); err != nil {
			t.Fatal(err)
		}
		direct := api.MikroTikPortForward{
			ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "app", UID: "direct-uid", Annotations: map[string]string{api.PublicIPAnnotation: "198.51.100.10"}},
			Spec:       api.MikroTikPortForwardSpec{RouterRef: "router-a", Protocol: "tcp", ExternalPort: 443, TargetAddress: "10.0.0.20", TargetPort: 8443},
		}
		differentRouter := api.MikroTikPortForward{
			ObjectMeta: metav1.ObjectMeta{Name: "different-router", Namespace: "app", UID: "different-uid", Annotations: map[string]string{api.PublicIPAnnotation: "198.51.100.10"}},
			Spec:       api.MikroTikPortForwardSpec{RouterRef: "router-b", Protocol: "tcp", ExternalPort: 443, TargetAddress: "10.0.0.30", TargetPort: 8443},
		}
		kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&parent, &generated, &direct, &differentRouter).Build()
		directCandidate := []portForwardCandidate{{protocol: "tcp", externalPort: 443, targetAddress: direct.Spec.TargetAddress, targetPort: direct.Spec.TargetPort}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &direct, direct.Spec.RouterRef, direct.Annotations[api.PublicIPAnnotation], nil, directCandidate); !errors.Is(err, errGeneratedClaimWaiting) {
			t.Fatalf("generated-first/direct-second did not select the direct port-forward claim: %v", err)
		}
		generatedCandidate := []portForwardCandidate{{name: generated.Name, protocol: "tcp", externalPort: 443, targetAddress: generated.Spec.TargetAddress, targetPort: generated.Spec.TargetPort}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &parent, generated.Spec.RouterRef, generated.Annotations[api.PublicIPAnnotation], nil, generatedCandidate); !errors.Is(err, errGeneratedChildCollision) {
			t.Fatalf("direct-first/generated-second did not reject the generated port-forward claim: %v", err)
		}
		differentCandidate := []portForwardCandidate{{protocol: "tcp", externalPort: 443, targetAddress: differentRouter.Spec.TargetAddress, targetPort: differentRouter.Spec.TargetPort}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &differentRouter, differentRouter.Spec.RouterRef, differentRouter.Annotations[api.PublicIPAnnotation], nil, differentCandidate); err != nil {
			t.Fatalf("same public tuple on a different Router was rejected: %v", err)
		}
	})

	t.Run("direct DNS convergence", func(t *testing.T) {
		scheme := controllerTestScheme(t)
		winner := api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "a-direct", Namespace: "app", UID: "winner-uid"},
			Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router", Name: "shared.example.com", Address: "10.0.0.10"},
		}
		loser := api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "b-direct", Namespace: "app", UID: "loser-uid"},
			Spec:       api.MikroTikDNSRecordSpec{RouterRef: "router", Name: "shared.example.com", Address: "10.0.0.20"},
		}
		kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&winner, &loser).Build()
		winnerCandidate := []generatedDNSCandidate{{hostname: winner.Spec.Name, address: winner.Spec.Address}}
		loserCandidate := []generatedDNSCandidate{{hostname: loser.Spec.Name, address: loser.Spec.Address}}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &winner, winner.Spec.RouterRef, "", winnerCandidate, nil); !errors.Is(err, errGeneratedClaimWaiting) {
			t.Fatalf("canonical direct winner did not wait for the loser to clean: %v", err)
		}
		_, loserErr := preflightGeneratedChildClaims(context.Background(), kube, &loser, loser.Spec.RouterRef, "", loserCandidate, nil)
		if !errors.Is(loserErr, errGeneratedChildCollision) {
			t.Fatalf("non-canonical direct claim did not yield: %v", loserErr)
		}
		loser.Status.Conditions = readyCondition(loser.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", loserErr.Error())
		if err := kube.Update(context.Background(), &loser); err != nil {
			t.Fatal(err)
		}
		if _, err := preflightGeneratedChildClaims(context.Background(), kube, &winner, winner.Spec.RouterRef, "", winnerCandidate, nil); err != nil {
			t.Fatalf("canonical direct winner remained blocked after the loser yielded: %v", err)
		}
	})
}

func TestCompactedTargetHistoryDoesNotRedialCleanedRouter(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dns",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-b"},
		},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: "router-b",
			Name:      "service.example.com",
			Address:   "10.0.0.10",
		},
		Status: api.MikroTikDNSRecordStatus{RouterRef: "router-a", Applied: true},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&record).
		WithStatusSubresource(&record).
		Build()
	dials := 0
	reconciler := DNSReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return nil, errors.New("cleaned router credentials were removed")
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "dns")); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("compacted cleanup history redialed an obsolete Router %d times", dials)
	}
	var stored api.MikroTikDNSRecord
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "app", Name: "dns"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.RouterRef != "" || stored.Status.Applied {
		t.Fatalf("obsolete status was not cleared after durable compaction: %#v", stored.Status)
	}
}
