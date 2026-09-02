package controller

import (
	"context"
	"errors"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestServiceDNSReconcilerCreatesOwnedRouteCRsWithoutRouterOS(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, node := annotatedClusterIPFixture()
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &node).Build()
	factoryCalls := 0
	reconciler := ServiceDNSReconciler{
		Client:        kube,
		RuntimeScheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			factoryCalls++
			return nil, errors.New("service reconciler must not call RouterOS")
		},
	}

	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 0 {
		t.Fatalf("Service reconciler called RouterOS %d times", factoryCalls)
	}

	routes := ownedRoutes(t, kube, &service)
	if len(routes) != 1 {
		t.Fatalf("got %d owned MikroTikRoute CRs, want 1", len(routes))
	}
	route := routes[0]
	if route.Spec.Destination != "10.0.0.8/32" {
		t.Fatalf("destination %q, want 10.0.0.8/32", route.Spec.Destination)
	}
	if route.Spec.Gateway != "192.0.2.10" {
		t.Fatalf("gateway %q, want 192.0.2.10", route.Spec.Gateway)
	}
	if route.Spec.RouterRef != router.Name {
		t.Fatalf("routerRef %q, want %q", route.Spec.RouterRef, router.Name)
	}
	if !metav1.IsControlledBy(&route, &service) {
		t.Fatal("MikroTikRoute is not owned by the Service")
	}
}

func TestServiceDNSReconcilerCreatesOneRoutePerNodeGateway(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, nodeA := annotatedClusterIPFixture()
	nodeB := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-b"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{
			Type: corev1.NodeInternalIP, Address: "192.0.2.11",
		}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &nodeA, &nodeB).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}
	routes := ownedRoutes(t, kube, &service)
	got := map[string]struct{}{}
	for _, route := range routes {
		got[route.Spec.Gateway] = struct{}{}
		if route.Spec.Destination != "10.0.0.8/32" {
			t.Fatalf("destination %q, want 10.0.0.8/32", route.Spec.Destination)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got gateways %#v, want 192.0.2.10 and 192.0.2.11", got)
	}
	if _, ok := got["192.0.2.10"]; !ok {
		t.Fatalf("missing gateway 192.0.2.10 in %#v", got)
	}
	if _, ok := got["192.0.2.11"]; !ok {
		t.Fatalf("missing gateway 192.0.2.11 in %#v", got)
	}
}

func TestServiceDNSReconcilerHonorsSingleNodeRouteMode(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, nodeA := annotatedClusterIPFixture()
	service.Annotations[api.RouteModeAnnotation] = "single-node"
	nodeB := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-b"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{
			Type: corev1.NodeInternalIP, Address: "192.0.2.11",
		}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &nodeA, &nodeB).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}
	routes := ownedRoutes(t, kube, &service)
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1 for single-node", len(routes))
	}
}

func TestServiceDNSReconcilerUpdatesRouteWhenClusterIPChanges(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, node := annotatedClusterIPFixture()
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &node).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}

	var stored corev1.Service
	if err := kube.Get(context.Background(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Spec.ClusterIP = "10.0.0.9"
	if err := kube.Update(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	if err := reconcileServiceUntil(t, reconciler, stored); err != nil {
		t.Fatal(err)
	}

	routes := ownedRoutes(t, kube, &stored)
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	if routes[0].Spec.Destination != "10.0.0.9/32" {
		t.Fatalf("destination %q, want 10.0.0.9/32", routes[0].Spec.Destination)
	}
}

func TestServiceDNSReconcilerDeletesRoutesWhenDNSAnnotationRemoved(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, node := annotatedClusterIPFixture()
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &node).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}
	if len(ownedRoutes(t, kube, &service)) == 0 {
		t.Fatal("expected owned route CRs before annotation removal")
	}

	var stored corev1.Service
	if err := kube.Get(context.Background(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, &stored); err != nil {
		t.Fatal(err)
	}
	delete(stored.Annotations, api.DNSNameAnnotation)
	if err := kube.Update(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	if err := reconcileServiceUntil(t, reconciler, stored); err != nil {
		t.Fatal(err)
	}
	if got := ownedRoutes(t, kube, &stored); len(got) != 0 {
		t.Fatalf("owned route CRs remained after annotation removal: %#v", got)
	}
}

func TestServiceDNSReconcilerDeletesRoutesOnServiceDeletion(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, node := annotatedClusterIPFixture()
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &node).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}

	var stored corev1.Service
	if err := kube.Get(context.Background(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, &stored); err != nil {
		t.Fatal(err)
	}
	if err := kube.Delete(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(service.Namespace, service.Name)); err != nil {
		t.Fatal(err)
	}
	if got := ownedRoutes(t, kube, &service); len(got) != 0 {
		t.Fatalf("owned route CRs remained after Service deletion: %#v", got)
	}
}

func TestServiceDNSReconcilerDeletesRoutesAndStripsLeftoverFinalizerWithoutGateways(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, _, _ := annotatedClusterIPFixture()
	now := metav1.Now()
	service.Finalizers = []string{serviceRouteFinalizer}
	service.DeletionTimestamp = &now
	owned := api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "rt-leftover", Namespace: service.Namespace},
		Spec:       api.MikroTikRouteSpec{Destination: "10.0.0.8/32", Gateway: "192.0.2.10"},
	}
	if err := controllerutil.SetControllerReference(&service, &owned, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &owned).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(service.Namespace, service.Name)); err != nil {
		t.Fatal(err)
	}
	if got := ownedRoutes(t, kube, &service); len(got) != 0 {
		t.Fatalf("owned route CRs remained after Service deletion: %#v", got)
	}
	var stored corev1.Service
	err := kube.Get(context.Background(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, &stored)
	if client.IgnoreNotFound(err) != nil {
		t.Fatal(err)
	}
	if err == nil && controllerutil.ContainsFinalizer(&stored, serviceRouteFinalizer) {
		t.Fatal("leftover service-route finalizer was not stripped")
	}
}

func TestServiceDNSReconcilerDoesNotCreateRouteForNodePort(t *testing.T) {
	scheme := controllerTestScheme(t)
	service, router, node := annotatedClusterIPFixture()
	service.Spec.Type = corev1.ServiceTypeNodePort
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &router, &node).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	if err := reconcileServiceUntil(t, reconciler, service); err != nil {
		t.Fatal(err)
	}
	if got := ownedRoutes(t, kube, &service); len(got) != 0 {
		t.Fatalf("NodePort Service created route CRs: %#v", got)
	}
}

func TestServiceDNSReconcilerCleansGeneratedChildrenWhenPublicIPRouterIsAmbiguous(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			UID:       "service-uid",
			Annotations: map[string]string{
				api.PublicIPAnnotation: "203.0.113.10",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.8",
			Ports:     []corev1.ServicePort{{Name: "http", Port: 80}},
		},
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
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "web-dns", Namespace: service.Namespace},
		Spec:       api.MikroTikDNSRecordSpec{Name: "web.home.arpa", Address: "10.0.0.8"},
	}
	if err := controllerutil.SetControllerReference(&service, &record, scheme); err != nil {
		t.Fatal(err)
	}
	leftover := api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "rt-leftover", Namespace: service.Namespace},
		Spec:       api.MikroTikRouteSpec{Destination: "10.0.0.8/32", Gateway: "192.0.2.10"},
	}
	if err := controllerutil.SetControllerReference(&service, &leftover, scheme); err != nil {
		t.Fatal(err)
	}
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "pf-leftover", Namespace: service.Namespace},
		Spec:       api.MikroTikPortForwardSpec{Protocol: "tcp", ExternalPort: 80, TargetPort: 80, TargetAddress: "10.0.0.8"},
	}
	if err := controllerutil.SetControllerReference(&service, &forward, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &first, &second, &record, &leftover, &forward).Build()
	reconciler := ServiceDNSReconciler{Client: kube, RuntimeScheme: scheme, Factory: refuseRouterOSFactory(t)}
	_, err := reconciler.Reconcile(context.Background(), reconcileRequest(service.Namespace, service.Name))
	if !errors.Is(err, errImplicitRouterSelection) {
		t.Fatalf("got %v, want %v", err, errImplicitRouterSelection)
	}
	assertNotFound(t, kube, &api.MikroTikDNSRecord{}, record.Namespace, record.Name)
	assertNotFound(t, kube, &api.MikroTikRoute{}, leftover.Namespace, leftover.Name)
	assertNotFound(t, kube, &api.MikroTikPortForward{}, forward.Namespace, forward.Name)
}

func TestIngressReconcilerCreatesOwnedRouteCRsWithoutRouterOS(t *testing.T) {
	scheme := controllerTestScheme(t)
	className := api.IngressClassName
	ingressClass := networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Name: api.IngressClassName},
		Spec:       networkingv1.IngressClassSpec{Controller: api.IngressController},
	}
	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "ingress-uid"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: "web.home.arpa",
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: pointerTo(networkingv1.PathTypePrefix),
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: "backend",
							Port: networkingv1.ServiceBackendPort{Number: 80},
						}},
					}},
				}},
			}},
		},
	}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8", Ports: []corev1.ServicePort{{Port: 80}}},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ingressClass, &ingress, &service, &router, &node).Build()
	factoryCalls := 0
	reconciler := IngressReconciler{
		Client:        kube,
		RuntimeScheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			factoryCalls++
			return nil, errors.New("ingress reconciler must not call RouterOS")
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(ingress.Namespace, ingress.Name)); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 0 {
		t.Fatalf("Ingress reconciler called RouterOS %d times", factoryCalls)
	}
	routes := ownedRoutes(t, kube, &ingress)
	if len(routes) != 1 {
		t.Fatalf("got %d owned MikroTikRoute CRs, want 1", len(routes))
	}
	if routes[0].Spec.Destination != "10.0.0.8/32" || routes[0].Spec.Gateway != "192.0.2.10" {
		t.Fatalf("unexpected route spec: %#v", routes[0].Spec)
	}
}

func TestDNSReconcilerCreatesOwnedRouteCRsForStandaloneServiceRef(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.1", CredentialsSecret: corev1.LocalObjectReference{Name: "creds"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"}, Data: map[string][]byte{"username": []byte("admin"), "password": []byte("x")}}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
	}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name: "standalone", Namespace: "app", UID: "dns-uid",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: router.Name, Name: "backend.home.arpa", Address: "10.0.0.8",
			ServiceRef: &api.NamespacedName{Namespace: "app", Name: "backend"},
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &service, &record, &node).
		WithStatusSubresource(&router, &record).Build()
	reconciler := DNSReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.ensuredRoutes != 0 {
		t.Fatalf("DNS reconciler called EnsureRoutes %d times", routerClient.ensuredRoutes)
	}
	if routerClient.ensuredDNS == 0 {
		t.Fatal("DNS reconciler did not apply the DNS record")
	}
	routes := ownedRoutes(t, kube, &record)
	if len(routes) != 1 {
		t.Fatalf("got %d owned MikroTikRoute CRs, want 1", len(routes))
	}
	if routes[0].Spec.Destination != "10.0.0.8/32" {
		t.Fatalf("destination %q, want 10.0.0.8/32", routes[0].Spec.Destination)
	}
}

func TestDNSReconcilerUsesNodePortInternalIP(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.1", CredentialsSecret: corev1.LocalObjectReference{Name: "creds"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"}, Data: map[string][]byte{"username": []byte("admin"), "password": []byte("x")}}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ClusterIP: "10.0.0.8", Ports: []corev1.ServicePort{{Port: 80, NodePort: 30080}}},
	}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nodeport", Namespace: "app", UID: "dns-uid",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: router.Name, Name: "backend.home.arpa",
			ServiceRef: &api.NamespacedName{Namespace: "app", Name: "backend"},
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &service, &record, &node).
		WithStatusSubresource(&router, &record).Build()
	reconciler := DNSReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredDNSAddresses) == 0 {
		t.Fatal("DNS reconciler did not apply the DNS record")
	}
	if got := routerClient.ensuredDNSAddresses[len(routerClient.ensuredDNSAddresses)-1]; got != "192.0.2.10" {
		t.Fatalf("EnsureDNS address = %q, want node InternalIP 192.0.2.10", got)
	}
}

func TestDNSReconcilerSkipsClusterRoutesWhenOwnedByService(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{Name: "primary", Address: "192.0.2.1", CredentialsSecret: corev1.LocalObjectReference{Name: "creds"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"}, Data: map[string][]byte{"username": []byte("admin"), "password": []byte("x")}}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app", UID: "svc-uid"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
	}
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name: "owned", Namespace: "app", UID: "dns-uid",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: router.Name, Name: "backend.home.arpa", Address: "10.0.0.8",
			ServiceRef: &api.NamespacedName{Namespace: "app", Name: "backend"},
		},
	}
	if err := controllerutil.SetControllerReference(&service, &record, scheme); err != nil {
		t.Fatal(err)
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &service, &record, &node).
		WithStatusSubresource(&router, &record).Build()
	reconciler := DNSReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.ensuredDNS == 0 {
		t.Fatal("DNS reconciler did not apply the DNS record")
	}
	if routes := ownedRoutes(t, kube, &record); len(routes) != 0 {
		t.Fatalf("translator-owned DNS created %d cluster routes, want 0", len(routes))
	}
}

func annotatedClusterIPFixture() (corev1.Service, api.MikroTikRouter, corev1.Node) {
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			UID:       "service-uid",
			Annotations: map[string]string{
				api.DNSNameAnnotation: "web.home.arpa",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.8",
			Ports:     []corev1.ServicePort{{Name: "http", Port: 80}},
		},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{
			Type: corev1.NodeInternalIP, Address: "192.0.2.10",
		}}},
	}
	return service, router, node
}

func reconcileServiceUntil(t *testing.T, reconciler ServiceDNSReconciler, service corev1.Service) error {
	t.Helper()
	req := reconcileRequest(service.Namespace, service.Name)
	for i := 0; i < 8; i++ {
		if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
			return err
		}
		var stored corev1.Service
		if err := reconciler.Get(context.Background(), req.NamespacedName, &stored); err != nil {
			return err
		}
		if len(ownedRoutes(t, reconciler.Client, &stored)) > 0 {
			return nil
		}
		if stored.Annotations[api.DNSNameAnnotation] == "" {
			return nil
		}
		if stored.Spec.Type != "" && stored.Spec.Type != corev1.ServiceTypeClusterIP {
			return nil
		}
	}
	return nil
}

func ownedRoutes(t *testing.T, kube client.Client, owner client.Object) []api.MikroTikRoute {
	t.Helper()
	var list api.MikroTikRouteList
	if err := kube.List(context.Background(), &list, client.InNamespace(owner.GetNamespace())); err != nil {
		t.Fatal(err)
	}
	owned := make([]api.MikroTikRoute, 0)
	for _, route := range list.Items {
		if metav1.IsControlledBy(&route, owner) {
			owned = append(owned, route)
		}
	}
	return owned
}

func refuseRouterOSFactory(t *testing.T) ros.Factory {
	t.Helper()
	return func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		t.Helper()
		return nil, errors.New("annotation controller must not call RouterOS")
	}
}
