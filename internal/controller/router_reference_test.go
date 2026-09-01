package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

func TestDNSReconcilerAppliesNamespaceNameRouterRef(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{
		Name:              "primary",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "local-router",
			Namespace:  "mikrotik",
			Finalizers: []string{resourceFinalizer},
		},
		Spec:   api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "mikrotik"}}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "ui-dns",
			Namespace:   "mikrotik-operator-system",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "mikrotik/local-router"},
		},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: "mikrotik/local-router",
			Name:      "ui.home.arpa",
			Address:   "10.0.0.8",
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &record).
		WithStatusSubresource(&router, &record).
		Build()
	reconciler := DNSReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikDNSRecord
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: record.Namespace, Name: record.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied {
		t.Fatalf("DNS record was not applied: %#v", stored.Status)
	}
	if routerClient.ensuredDNS == 0 {
		t.Fatal("expected DNS apply against the cross-namespace router")
	}
}

func TestWithRouterConnectionsResolvesSlashNameKey(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{
		Name:              "primary",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "local-router",
			Namespace:  "mikrotik",
			Finalizers: []string{resourceFinalizer},
		},
		Spec:   api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "mikrotik"}}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret).Build()
	kube := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, underlying client.WithWatch, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*api.MikroTikRouter); ok {
				if strings.Contains(key.Name, "/") {
					t.Fatalf("Get used slash in object name: %#v", key)
				}
				want := types.NamespacedName{Namespace: "mikrotik", Name: "local-router"}
				if key != want {
					t.Fatalf("Get key %#v, want %#v", key, want)
				}
			}
			return underlying.Get(ctx, key, obj, opts...)
		},
	})
	err := withRouterConnections(
		context.Background(),
		kube,
		func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return &recordingRouterClient{}, nil
		},
		types.NamespacedName{Namespace: "mikrotik-operator-system", Name: "mikrotik/local-router"},
		true,
		func(got api.MikroTikRouter, _ []routerConnection) error {
			if got.Namespace != "mikrotik" || got.Name != "local-router" {
				t.Fatalf("connected router %s/%s, want mikrotik/local-router", got.Namespace, got.Name)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected slash-form router key to resolve, got %v", err)
	}
}

func TestResolveRouterReferenceFindsExplicitRouterWhenGetMisses(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "local-router", Namespace: "mikrotik"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.168.88.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	kube := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, underlying client.WithWatch, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*api.MikroTikRouter); ok {
				return apierrors.NewNotFound(
					api.GroupVersion.WithResource("mikrotikrouters").GroupResource(),
					key.Name,
				)
			}
			return underlying.Get(ctx, key, obj)
		},
	})
	got, err := resolveRouterReference(context.Background(), kube, "mikrotik-operator-system", "mikrotik/local-router")
	if err != nil {
		t.Fatal(err)
	}
	want := types.NamespacedName{Namespace: "mikrotik", Name: "local-router"}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestRouterKeyFromRefNeverReturnsSlashName(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		reference string
		want      types.NamespacedName
	}{
		{
			name:      "namespace/name across namespaces",
			namespace: "mikrotik-operator-system",
			reference: "mikrotik/local-router",
			want:      types.NamespacedName{Namespace: "mikrotik", Name: "local-router"},
		},
		{
			name:      "bare name in resource namespace",
			namespace: "mikrotik",
			reference: "local-router",
			want:      types.NamespacedName{Namespace: "mikrotik", Name: "local-router"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := routerKeyFromRef(test.namespace, test.reference)
			if got != test.want {
				t.Fatalf("routerKeyFromRef(%q, %q)=%#v want %#v", test.namespace, test.reference, got, test.want)
			}
			if strings.Contains(got.Name, "/") {
				t.Fatalf("router key name %q still contains a slash", got.Name)
			}
		})
	}
}

func TestGetMikroTikRouterRejectsSlashInName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	kube := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, underlying client.WithWatch, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*api.MikroTikRouter); ok {
				t.Fatalf("Get must not be called for malformed router name: %#v", key)
			}
			return underlying.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, underlying client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*api.MikroTikRouterList); ok {
				t.Fatal("List must not be called for malformed router name")
			}
			return underlying.List(ctx, list, opts...)
		},
	})
	_, err := getMikroTikRouter(context.Background(), kube, types.NamespacedName{
		Namespace: "app",
		Name:      "foo/bar/baz",
	})
	if err == nil {
		t.Fatal("expected not-found for extra slashes in router name")
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("error %v is not a not-found error", err)
	}
}
