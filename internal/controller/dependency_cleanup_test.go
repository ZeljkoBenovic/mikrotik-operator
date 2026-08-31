package controller

import (
	"context"
	"errors"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestDNSNonAddressableServiceCleansEveryDurableRouter(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dns", Namespace: "app", Finalizers: []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a,router-b"},
		},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: "router-b", Name: "service.example.com",
			ServiceRef: &api.NamespacedName{Namespace: "app", Name: "service"},
		},
	}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "service", Namespace: "app"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "external.example.com"},
	}
	objects = append(objects, &record, &service)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&record).Build()
	reconciler := DNSReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "dns")); err != nil {
		t.Fatal(err)
	}
	for name, routerClient := range clients {
		if routerClient.deletedDNS == 0 {
			t.Fatalf("%s was not fully cleaned: DNS=%d", name, routerClient.deletedDNS)
		}
	}
}

func TestPortForwardNonAddressableServiceCleansEveryDurableRouter(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name: "forward", Namespace: "app", Finalizers: []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a,router-b"},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef: "router-b", Protocol: "tcp", ExternalPort: 80, TargetPort: 80,
			ServiceRef: &api.NamespacedName{Namespace: "app", Name: "service"},
		},
	}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "service", Namespace: "app"},
		Spec:       corev1.ServiceSpec{ClusterIP: corev1.ClusterIPNone},
	}
	objects = append(objects, &forward, &service)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&forward).Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "forward")); err != nil {
		t.Fatal(err)
	}
	for name, routerClient := range clients {
		if routerClient.deletedForwards == 0 || routerClient.deletedFirewall == 0 {
			t.Fatalf("%s was not fully cleaned: forwards=%d firewall=%d", name, routerClient.deletedForwards, routerClient.deletedFirewall)
		}
	}
}

func TestPodPortForwardCleanupSurvivesApplyStatusConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	endpoint := api.RouterEndpoint{
		Name:              "primary",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
		Status:     corev1.PodStatus{PodIP: "10.0.0.20"},
	}
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "forward",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:    router.Name,
			Protocol:     "tcp",
			ExternalPort: 8080,
			TargetPort:   8080,
			PodRef:       &api.NamespacedName{Namespace: pod.Namespace, Name: pod.Name},
		},
	}
	routerClient := &recordingRouterClient{}
	factory := func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &pod, &forward).
		WithStatusSubresource(&router, &forward).
		Build()
	conflict := apierrors.NewConflict(
		schema.GroupResource{Group: api.GroupVersion.Group, Resource: "mikrotikportforwards"},
		forward.Name,
		errors.New("concurrent status update"),
	)
	kube := interceptor.NewClient(base, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, underlying client.Client, subresource string, object client.Object, options ...client.SubResourceUpdateOption) error {
			if subresource == "status" {
				if _, ok := object.(*api.MikroTikPortForward); ok {
					return conflict
				}
			}
			return underlying.SubResource(subresource).Update(ctx, object, options...)
		},
	})
	reconciler := PortForwardReconciler{Client: kube, Factory: factory}
	request := reconcileRequest(forward.Namespace, forward.Name)
	if _, err := reconciler.Reconcile(context.Background(), request); !apierrors.IsConflict(err) {
		t.Fatalf("got error %v, want apply status conflict", err)
	}
	if routerClient.ensuredForwards == 0 || routerClient.ensuredFirewall == 0 {
		t.Fatalf("port forward was not applied before status failed: forwards=%d firewall=%d", routerClient.ensuredForwards, routerClient.ensuredFirewall)
	}
	var stored api.MikroTikPortForward
	if err := base.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.TargetAddress != "" {
		t.Fatalf("status unexpectedly persisted target address %q", stored.Status.TargetAddress)
	}
	if err := base.Delete(context.Background(), &pod); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); !apierrors.IsConflict(err) {
		t.Fatalf("got error %v, want cleanup status conflict", err)
	}
	if routerClient.deletedForwards == 0 || routerClient.deletedFirewall == 0 {
		t.Fatalf("Pod deletion did not clean durable RouterOS state: forwards=%d firewall=%d", routerClient.deletedForwards, routerClient.deletedFirewall)
	}
}

func TestGeneratedDNSRecordLosesToDirectClaimAndCleansOnlyItself(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	parent := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "generated-owner", Namespace: "app", UID: "generated-owner-uid"}}
	generated := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "generated",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec:   api.MikroTikDNSRecordSpec{RouterRef: router.Name, Name: "shared.example.com", Address: "10.0.0.10"},
		Status: api.MikroTikDNSRecordStatus{RouterRef: router.Name, Applied: true},
	}
	if err := controllerutil.SetControllerReference(&parent, &generated, scheme); err != nil {
		t.Fatal(err)
	}
	direct := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "app", UID: "direct-uid"},
		Spec:       api.MikroTikDNSRecordSpec{RouterRef: router.Name, Name: generated.Spec.Name, Address: "10.0.0.20"},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &parent, &generated, &direct).
		WithStatusSubresource(&router, &generated).
		Build()
	reconciler := DNSReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(generated.Namespace, generated.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.deletedDNS == 0 {
		t.Fatalf("losing generated DNS state was not cleaned: DNS=%d", routerClient.deletedDNS)
	}
	var storedGenerated api.MikroTikDNSRecord
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: generated.Namespace, Name: generated.Name}, &storedGenerated); err != nil {
		t.Fatal(err)
	}
	if storedGenerated.Status.Applied {
		t.Fatal("losing generated DNS record remained Applied")
	}
	assertExists(t, kube, &api.MikroTikDNSRecord{}, direct.Namespace, direct.Name)
}

func TestGeneratedPortForwardLosesToDirectClaimAndCleansOnlyItself(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	parent := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "generated-owner", Namespace: "app", UID: "generated-owner-uid"}}
	generated := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "generated",
			Namespace:  "app",
			Finalizers: []string{resourceFinalizer},
			Annotations: map[string]string{
				durableRouterTargetsAnnotation: router.Name,
				api.PublicIPAnnotation:         "198.51.100.10",
			},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef: router.Name, Protocol: "tcp", ExternalPort: 443,
			TargetAddress: "10.0.0.10", TargetPort: 8443,
		},
		Status: api.MikroTikPortForwardStatus{RouterRef: router.Name, Applied: true, TargetAddress: "10.0.0.10"},
	}
	if err := controllerutil.SetControllerReference(&parent, &generated, scheme); err != nil {
		t.Fatal(err)
	}
	direct := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "app", UID: "direct-uid", Annotations: map[string]string{api.PublicIPAnnotation: generated.Annotations[api.PublicIPAnnotation]}},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef: router.Name, Protocol: "tcp", ExternalPort: 443,
			TargetAddress: "10.0.0.20", TargetPort: 8443,
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &parent, &generated, &direct).
		WithStatusSubresource(&router, &generated).
		Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(generated.Namespace, generated.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.deletedForwards == 0 || routerClient.deletedFirewall == 0 {
		t.Fatalf("losing generated port-forward state was not cleaned: forwards=%d firewall=%d", routerClient.deletedForwards, routerClient.deletedFirewall)
	}
	var storedGenerated api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: generated.Namespace, Name: generated.Name}, &storedGenerated); err != nil {
		t.Fatal(err)
	}
	if storedGenerated.Status.Applied {
		t.Fatal("losing generated port forward remained Applied")
	}
	assertExists(t, kube, &api.MikroTikPortForward{}, direct.Namespace, direct.Name)
}

func TestDirectClaimTransientReadsPreserveAppliedState(t *testing.T) {
	t.Run("DNS claim list", func(t *testing.T) {
		scheme := controllerTestScheme(t)
		endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
		router := api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}}, Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}}, Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}}}
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
		record := api.MikroTikDNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "app", Finalizers: []string{resourceFinalizer}, Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name}},
			Spec:       api.MikroTikDNSRecordSpec{RouterRef: router.Name, Name: "direct.example.com", Address: "10.0.0.10"},
			Status:     api.MikroTikDNSRecordStatus{RouterRef: router.Name, Applied: true},
		}
		base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &record).WithStatusSubresource(&router, &record).Build()
		sentinel := errors.New("transient DNS claim list")
		kube := interceptor.NewClient(base, interceptor.Funcs{
			List: func(ctx context.Context, underlying client.WithWatch, list client.ObjectList, options ...client.ListOption) error {
				if _, ok := list.(*api.MikroTikDNSRecordList); ok {
					return sentinel
				}
				return underlying.List(ctx, list, options...)
			},
		})
		routerClient := &recordingRouterClient{}
		reconciler := DNSReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		}}
		if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); !errors.Is(err, sentinel) {
			t.Fatalf("got error %v, want transient claim-list error", err)
		}
		if routerClient.deletedDNS != 0 || routerClient.deletedRoutes != 0 {
			t.Fatalf("transient claim read deleted applied DNS state: DNS=%d routes=%d", routerClient.deletedDNS, routerClient.deletedRoutes)
		}
		var stored api.MikroTikDNSRecord
		if err := base.Get(context.Background(), types.NamespacedName{Namespace: record.Namespace, Name: record.Name}, &stored); err != nil {
			t.Fatal(err)
		}
		if !stored.Status.Applied {
			t.Fatal("transient claim read cleared Applied status")
		}
	})

	t.Run("implicit Router resolution", func(t *testing.T) {
		scheme := controllerTestScheme(t)
		endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
		router := api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}}, Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}}, Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}}}
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
		forward := api.MikroTikPortForward{
			ObjectMeta: metav1.ObjectMeta{Name: "direct", Namespace: "app", Finalizers: []string{resourceFinalizer}, Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name}},
			Spec:       api.MikroTikPortForwardSpec{Protocol: "tcp", ExternalPort: 443, TargetAddress: "10.0.0.10", TargetPort: 8443},
			Status:     api.MikroTikPortForwardStatus{RouterRef: router.Name, Applied: true, TargetAddress: "10.0.0.10"},
		}
		base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &forward).WithStatusSubresource(&router, &forward).Build()
		sentinel := errors.New("transient Router resolution")
		kube := interceptor.NewClient(base, interceptor.Funcs{
			List: func(ctx context.Context, underlying client.WithWatch, list client.ObjectList, options ...client.ListOption) error {
				if _, ok := list.(*api.MikroTikRouterList); ok {
					return sentinel
				}
				return underlying.List(ctx, list, options...)
			},
		})
		routerClient := &recordingRouterClient{}
		reconciler := PortForwardReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		}}
		if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); !errors.Is(err, sentinel) {
			t.Fatalf("got error %v, want transient Router-resolution error", err)
		}
		if routerClient.deletedForwards != 0 || routerClient.deletedFirewall != 0 {
			t.Fatalf("transient Router resolution deleted applied port-forward state: forwards=%d firewall=%d", routerClient.deletedForwards, routerClient.deletedFirewall)
		}
		var stored api.MikroTikPortForward
		if err := base.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
			t.Fatal(err)
		}
		if !stored.Status.Applied {
			t.Fatal("transient Router resolution cleared Applied status")
		}
	})
}

func externalCleanupFixture(t *testing.T) (*runtime.Scheme, []client.Object, ros.Factory, map[string]*recordingRouterClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	routerA := &api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{Name: "router-a", Namespace: "app"}, Spec: api.MikroTikRouterSpec{Address: "192.0.2.1", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}}
	routerB := &api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{Name: "router-b", Namespace: "app"}, Spec: api.MikroTikRouterSpec{Address: "192.0.2.2", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	clients := map[string]*recordingRouterClient{
		"router-a": {},
		"router-b": {},
	}
	factory := func(_ context.Context, address string, _ int32, _ bool, _, _ string) (ros.Client, error) {
		if address == routerA.Spec.Address {
			return clients["router-a"], nil
		}
		return clients["router-b"], nil
	}
	return scheme, []client.Object{routerA, routerB, secret}, factory, clients
}

type recordingRouterClient struct {
	ensuredDNS           int
	ensuredRoutes        int
	ensuredForwards      int
	ensuredFirewall      int
	deletedDNS           int
	deletedRoutes        int
	deletedForwards      int
	deletedFirewall      int
	ensuredRouteGateways []string
	deletedRouteComments []string
	ensuredPortForwards  []ros.PortForward
}

func (client *recordingRouterClient) EnsureDNS(context.Context, string, string, string, string) error {
	client.ensuredDNS++
	return nil
}
func (client *recordingRouterClient) DeleteDNS(context.Context, string) error {
	client.deletedDNS++
	return nil
}

func (client *recordingRouterClient) EnsurePortForward(_ context.Context, forward ros.PortForward, _ string) error {
	client.ensuredForwards++
	client.ensuredPortForwards = append(client.ensuredPortForwards, forward)
	return nil
}
func (client *recordingRouterClient) DeletePortForward(context.Context, string) error {
	client.deletedForwards++
	return nil
}
func (*recordingRouterClient) EnsureRoute(context.Context, string, string, string) error { return nil }
func (client *recordingRouterClient) EnsureRouteWithDistance(_ context.Context, _, gateway string, _ int32, _ string) error {
	client.ensuredRouteGateways = append(client.ensuredRouteGateways, gateway)
	return nil
}
func (client *recordingRouterClient) EnsureRoutes(context.Context, string, []string, string) error {
	client.ensuredRoutes++
	return nil
}
func (client *recordingRouterClient) DeleteRoute(_ context.Context, comment string) error {
	client.deletedRouteComments = append(client.deletedRouteComments, comment)
	return nil
}
func (client *recordingRouterClient) DeleteRoutesByPrefix(context.Context, string) error {
	client.deletedRoutes++
	return nil
}

func (client *recordingRouterClient) EnsureFirewallRule(context.Context, ros.FirewallRule, string) error {
	client.ensuredFirewall++
	return nil
}
func (client *recordingRouterClient) DeleteFirewallRule(context.Context, string) error {
	client.deletedFirewall++
	return nil
}
func (*recordingRouterClient) Close() error { return nil }
