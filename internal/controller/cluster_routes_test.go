package controller

import (
	"context"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
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
