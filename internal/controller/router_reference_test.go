package controller

import (
	"context"
	"errors"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveRouterReferenceSelectsUniqueClusterRouter(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	got, err := resolveRouterReference(context.Background(), kube, "mikrotik-operator-system", "")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "network", Name: "edge"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveRouterReferencePrefersRouterInResourceNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	local := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "local", Namespace: "app"},
		Spec: api.MikroTikRouterSpec{
			Address:           "10.0.0.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	other := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&local, &other).Build()
	got, err := resolveRouterReference(context.Background(), kube, "app", "")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "app", Name: "local"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveRouterReferenceRequiresExplicitRefWhenMultipleClusterRouters(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	first := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	second := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "other"},
		Spec: api.MikroTikRouterSpec{
			Address:           "10.0.0.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&first, &second).Build()
	_, err := resolveRouterReference(context.Background(), kube, "app", "")
	if !errors.Is(err, errImplicitRouterSelection) {
		t.Fatalf("got %v want %v", err, errImplicitRouterSelection)
	}
}

func TestResolveRouterReferenceFindsNamedRouterInAnotherNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	got, err := resolveRouterReference(context.Background(), kube, "app", "edge")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "network", Name: "edge"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveRouterReferenceHonorsExplicitNamespaceName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	got, err := resolveRouterReference(context.Background(), kube, "app", "network/edge")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "network", Name: "edge"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestServiceDNSReconcilerResolvesCrossNamespaceRouter(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mikrotik-operator-mikrotik-operator-ui",
			Namespace: "mikrotik-operator-system",
			Annotations: map[string]string{
				api.DNSNameAnnotation: "ui.home.arpa",
			},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	reconciler := ServiceDNSReconciler{Client: kube}
	got, err := reconciler.resolveRouterRef(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "network", Name: "edge"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveRouterReferenceSkipsTerminatingClusterRouter(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := metav1.Now()
	terminating := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "old",
			Namespace:         "other",
			DeletionTimestamp: &now,
			Finalizers:        []string{"mikrotik.operator.io/finalizer"},
		},
		Spec: api.MikroTikRouterSpec{
			Address:           "10.0.0.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	live := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&terminating, &live).Build()
	got, err := resolveRouterReference(context.Background(), kube, "app", "")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "network", Name: "edge"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestGeneratedClaimRouterRefsUsesRouterNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "ui-dns", Namespace: "mikrotik-operator-system"},
		Spec:       api.MikroTikDNSRecordSpec{Name: "ui.home.arpa", Address: "10.0.0.8"},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &record).Build()
	got, err := generatedClaimRouterRefs(context.Background(), kube, record.Namespace, &record, record.Status.RouterRef, record.Spec.RouterRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "network/edge" {
		t.Fatalf("got %#v want [network/edge]", got)
	}
}

func TestRouterRefStorageUsesNamespaceNameAcrossNamespaces(t *testing.T) {
	same := routerRefStorage("network", types.NamespacedName{Namespace: "network", Name: "edge"})
	if same != "edge" {
		t.Fatalf("same-namespace storage got %q want edge", same)
	}
	cross := routerRefStorage("mikrotik-operator-system", types.NamespacedName{Namespace: "network", Name: "edge"})
	if cross != "network/edge" {
		t.Fatalf("cross-namespace storage got %q want network/edge", cross)
	}
}

func TestResolveRouterReferenceKeepsNamedRefWhenRouterIsMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	got, err := resolveRouterReference(context.Background(), kube, "app", "edge")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "app", Name: "edge"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
