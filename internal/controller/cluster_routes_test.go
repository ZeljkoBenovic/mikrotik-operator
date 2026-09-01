package controller

import (
	"context"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestClusterRouteGateways(t *testing.T) {
	backend := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	ownerRouter := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "edge"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
			RouteGateway:      "10.8.8.8",
		},
	}
	serviceNSRouter := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "app"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.0.2.2",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
			RouteGateway:      "10.9.9.9",
		},
	}
	multiEndpointRouter := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "edge"},
		Spec: api.MikroTikRouterSpec{
			Routers: []api.RouterEndpoint{
				{
					Name:              "a",
					Address:           "192.0.2.1",
					CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
					RouteGateway:      "10.1.1.1",
				},
				{
					Name:              "b",
					Address:           "192.0.2.2",
					CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
					RouteGateway:      "10.1.1.2",
				},
			},
		},
	}
	mixedGatewayRouter := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "edge"},
		Spec: api.MikroTikRouterSpec{
			RouteGateway: "10.9.9.9",
			Routers: []api.RouterEndpoint{
				{
					Name:              "a",
					Address:           "192.0.2.1",
					CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
					RouteGateway:      "10.1.1.1",
				},
				{
					Name:              "b",
					Address:           "192.0.2.2",
					CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
				},
			},
		},
	}

	tests := []struct {
		name           string
		ownerNamespace string
		routerRef      string
		objects        []client.Object
		want           []string
		wantNotFound   bool
	}{
		{
			name:           "resolves routerRef in owner namespace",
			ownerNamespace: "edge",
			routerRef:      "core",
			objects:        []client.Object{&ownerRouter, &serviceNSRouter, &node},
			want:           []string{"10.8.8.8"},
		},
		{
			name:           "missing router with routerRef is an error",
			ownerNamespace: "edge",
			routerRef:      "missing",
			objects:        []client.Object{&node},
			wantNotFound:   true,
		},
		{
			name:           "resolves namespace/name routerRef in another namespace",
			ownerNamespace: "app",
			routerRef:      "edge/core",
			objects:        []client.Object{&ownerRouter, &serviceNSRouter, &node},
			want:           []string{"10.8.8.8"},
		},
		{
			name:           "honors per-endpoint routeGateway",
			ownerNamespace: "edge",
			routerRef:      "core",
			objects:        []client.Object{&multiEndpointRouter, &node},
			want:           []string{"10.1.1.1", "10.1.1.2"},
		},
		{
			name:           "falls back to spec.routeGateway per endpoint",
			ownerNamespace: "edge",
			routerRef:      "core",
			objects:        []client.Object{&mixedGatewayRouter, &node},
			want:           []string{"10.1.1.1", "10.9.9.9"},
		},
		{
			name:           "keeps node IPs for endpoints without routeGateway",
			ownerNamespace: "edge",
			routerRef:      "core",
			objects: []client.Object{
				&api.MikroTikRouter{
					ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "edge"},
					Spec: api.MikroTikRouterSpec{
						Routers: []api.RouterEndpoint{
							{
								Name:              "a",
								Address:           "192.0.2.1",
								CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
								RouteGateway:      "10.1.1.1",
							},
							{
								Name:              "b",
								Address:           "192.0.2.2",
								CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
							},
						},
					},
				},
				&node,
			},
			want: []string{"10.1.1.1", "192.0.2.10"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := controllerTestScheme(t)
			objects := append([]client.Object{backend.DeepCopy()}, test.objects...)
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			got, err := clusterRouteGateways(context.Background(), kube, backend, test.ownerNamespace, test.routerRef)
			if test.wantNotFound {
				if err == nil {
					t.Fatalf("got gateways %#v, want not-found error", got)
				}
				if !apierrors.IsNotFound(err) {
					t.Fatalf("error %v is not a not-found error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !stringSetEqual(got, test.want) {
				t.Fatalf("got gateways %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReconcileOwnedClusterRoutesDeletesEmptyDesiredSet(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "service-uid"},
	}
	owned := api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "rt-leftover", Namespace: service.Namespace},
		Spec:       api.MikroTikRouteSpec{Destination: "10.0.0.8/32", Gateway: "192.0.2.10"},
	}
	if err := controllerutil.SetControllerReference(&service, &owned, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &owned).Build()
	if err := reconcileOwnedClusterRoutes(context.Background(), clusterRouteReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      &service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
	}); err != nil {
		t.Fatal(err)
	}
	var list api.MikroTikRouteList
	if err := kube.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("owned routes remained: %#v", list.Items)
	}
}

func TestClusterRouteAppliesToEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		router   api.MikroTikRouter
		gateway  string
		origin   string
		endpoint int
		want     bool
	}{
		{
			name: "configured endpoint keeps its gateway",
			router: api.MikroTikRouter{Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{
				{Name: "a", Address: "192.0.2.1", RouteGateway: "10.1.1.1"},
				{Name: "b", Address: "192.0.2.2", RouteGateway: "10.1.1.2"},
			}}},
			gateway:  "10.1.1.1",
			origin:   clusterRouteOriginOverride,
			endpoint: 0,
			want:     true,
		},
		{
			name: "configured endpoint rejects peer gateway",
			router: api.MikroTikRouter{Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{
				{Name: "a", Address: "192.0.2.1", RouteGateway: "10.1.1.1"},
				{Name: "b", Address: "192.0.2.2", RouteGateway: "10.1.1.2"},
			}}},
			gateway:  "10.1.1.2",
			origin:   clusterRouteOriginOverride,
			endpoint: 0,
			want:     false,
		},
		{
			name: "unconfigured endpoint keeps a hop that is both node IP and peer routeGateway",
			router: api.MikroTikRouter{Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{
				{Name: "a", Address: "192.0.2.1", RouteGateway: "192.0.2.10"},
				{Name: "b", Address: "192.0.2.2"},
			}}},
			gateway:  "192.0.2.10",
			origin:   clusterRouteOriginBoth,
			endpoint: 1,
			want:     true,
		},
		{
			name: "unconfigured endpoint rejects peer routeGateway that is not a selected node hop",
			router: api.MikroTikRouter{Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{
				{Name: "a", Address: "192.0.2.1", RouteGateway: "192.0.2.11"},
				{Name: "b", Address: "192.0.2.2"},
			}}},
			gateway:  "192.0.2.11",
			origin:   clusterRouteOriginOverride,
			endpoint: 1,
			want:     false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := test.router.Spec.Routers[test.endpoint]
			if got := clusterRouteAppliesToEndpoint(test.gateway, test.origin, endpoint, test.router); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestRouteReconcilerAppliesGeneratedClusterRoutesPerEndpoint(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoints := []api.RouterEndpoint{
		{
			Name:              "a",
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
			RouteGateway:      "10.1.1.1",
		},
		{
			Name:              "b",
			Address:           "192.0.2.2",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
			RouteGateway:      "10.1.1.2",
		},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: endpoints},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: endpoints},
	}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("x")},
	}
	routeA := generatedClusterRoute("rt-a", "10.1.1.1", clusterRouteOriginOverride)
	routeB := generatedClusterRoute("rt-b", "10.1.1.2", clusterRouteOriginOverride)
	clients := map[string]*recordingRouterClient{
		"192.0.2.1": {},
		"192.0.2.2": {},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &routeA, &routeB).
		WithStatusSubresource(&router, &routeA, &routeB).Build()
	reconciler := RouteReconciler{
		Client: kube,
		Factory: func(_ context.Context, address string, _ int32, _ bool, _, _ string) (ros.Client, error) {
			client, ok := clients[address]
			if !ok {
				t.Fatalf("unexpected router address %s", address)
			}
			return client, nil
		},
	}
	for _, name := range []string{"rt-a", "rt-b"} {
		if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", name)); err != nil {
			t.Fatal(err)
		}
	}
	if !stringSetEqual(clients["192.0.2.1"].ensuredRouteGateways, []string{"10.1.1.1"}) {
		t.Fatalf("endpoint a gateways %#v, want [10.1.1.1]", clients["192.0.2.1"].ensuredRouteGateways)
	}
	if !stringSetEqual(clients["192.0.2.2"].ensuredRouteGateways, []string{"10.1.1.2"}) {
		t.Fatalf("endpoint b gateways %#v, want [10.1.1.2]", clients["192.0.2.2"].ensuredRouteGateways)
	}
}

func TestRouteReconcilerKeepsNodeIPOnUnconfiguredEndpointWhenPeerUsesSameGateway(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoints := []api.RouterEndpoint{
		{
			Name:              "a",
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
			RouteGateway:      "192.0.2.10",
		},
		{
			Name:              "b",
			Address:           "192.0.2.2",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: endpoints},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: endpoints},
	}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("x")},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	route := generatedClusterRoute("rt-shared", "192.0.2.10", clusterRouteOriginBoth)
	clients := map[string]*recordingRouterClient{
		"192.0.2.1": {},
		"192.0.2.2": {},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &node, &route).
		WithStatusSubresource(&router, &route).Build()
	reconciler := RouteReconciler{
		Client: kube,
		Factory: func(_ context.Context, address string, _ int32, _ bool, _, _ string) (ros.Client, error) {
			client, ok := clients[address]
			if !ok {
				t.Fatalf("unexpected router address %s", address)
			}
			return client, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", route.Name)); err != nil {
		t.Fatal(err)
	}
	if !stringSetEqual(clients["192.0.2.1"].ensuredRouteGateways, []string{"192.0.2.10"}) {
		t.Fatalf("configured endpoint gateways %#v, want [192.0.2.10]", clients["192.0.2.1"].ensuredRouteGateways)
	}
	if !stringSetEqual(clients["192.0.2.2"].ensuredRouteGateways, []string{"192.0.2.10"}) {
		t.Fatalf("unconfigured endpoint gateways %#v, want [192.0.2.10]", clients["192.0.2.2"].ensuredRouteGateways)
	}
}

func TestRouteReconcilerSkipsPeerNodeIPOverrideOnUnconfiguredEndpoint(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoints := []api.RouterEndpoint{
		{
			Name:              "a",
			Address:           "192.0.2.1",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
			RouteGateway:      "192.0.2.11",
		},
		{
			Name:              "b",
			Address:           "192.0.2.2",
			CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: endpoints},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: endpoints},
	}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("x")},
	}
	route := generatedClusterRoute("rt-override", "192.0.2.11", clusterRouteOriginOverride)
	clients := map[string]*recordingRouterClient{
		"192.0.2.1": {},
		"192.0.2.2": {},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret, &route).
		WithStatusSubresource(&router, &route).Build()
	reconciler := RouteReconciler{
		Client: kube,
		Factory: func(_ context.Context, address string, _ int32, _ bool, _, _ string) (ros.Client, error) {
			client, ok := clients[address]
			if !ok {
				t.Fatalf("unexpected router address %s", address)
			}
			return client, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", route.Name)); err != nil {
		t.Fatal(err)
	}
	if !stringSetEqual(clients["192.0.2.1"].ensuredRouteGateways, []string{"192.0.2.11"}) {
		t.Fatalf("configured endpoint gateways %#v, want [192.0.2.11]", clients["192.0.2.1"].ensuredRouteGateways)
	}
	if len(clients["192.0.2.2"].ensuredRouteGateways) != 0 {
		t.Fatalf("unconfigured endpoint installed override %#v", clients["192.0.2.2"].ensuredRouteGateways)
	}
}

func TestClusterRouteHopsMarksOverrideVersusSingleNodeOrigin(t *testing.T) {
	scheme := controllerTestScheme(t)
	backend := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app", Annotations: map[string]string{api.RouteModeAnnotation: "single-node"}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "core", Namespace: "edge"},
		Spec: api.MikroTikRouterSpec{
			Routers: []api.RouterEndpoint{
				{Name: "a", Address: "192.0.2.1", CredentialsSecret: corev1.LocalObjectReference{Name: "creds"}, RouteGateway: "192.0.2.11"},
				{Name: "b", Address: "192.0.2.2", CredentialsSecret: corev1.LocalObjectReference{Name: "creds"}},
			},
		},
	}
	nodeA := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	nodeB := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-b"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.11"}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&backend, &router, &nodeA, &nodeB).Build()
	hops, err := clusterRouteHops(context.Background(), kube, backend, "edge", "core")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, hop := range hops {
		got[hop.gateway] = hop.origin
	}
	if got["192.0.2.11"] != clusterRouteOriginOverride {
		t.Fatalf("peer node IP origin %q, want override", got["192.0.2.11"])
	}
	if got["192.0.2.10"] != clusterRouteOriginNodes {
		t.Fatalf("single-node origin %q, want nodes", got["192.0.2.10"])
	}
}

func generatedClusterRoute(name, gateway, origin string) api.MikroTikRoute {
	return api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "app",
			Finalizers: []string{resourceFinalizer},
			Labels: map[string]string{
				clusterRouteSourceLabel: "src",
				clusterRouteOriginLabel: origin,
			},
			Annotations: map[string]string{
				durableRouterTargetsAnnotation: "core",
			},
		},
		Spec: api.MikroTikRouteSpec{
			RouterRef:   "core",
			Destination: "10.0.0.8/32",
			Gateway:     gateway,
		},
	}
}

func stringSetEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}
