package controller

import (
	"context"
	"strings"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func routeGatewayService() corev1.Service {
	return corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
}

func routeGatewayNode(name string, addresses ...corev1.NodeAddress) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Addresses: addresses},
	}
}

func TestRouteGateways(t *testing.T) {
	t.Parallel()

	t.Run("rejects unsupported route-mode", func(t *testing.T) {
		t.Parallel()
		kube := fake.NewClientBuilder().WithScheme(controllerTestScheme(t)).Build()
		service := routeGatewayService()
		service.Annotations = map[string]string{api.RouteModeAnnotation: "bogus"}
		_, err := routeGateways(context.Background(), kube, service)
		if err == nil || !strings.Contains(err.Error(), api.RouteModeAnnotation) {
			t.Fatalf("error = %v, want unsupported %s", err, api.RouteModeAnnotation)
		}
	})

	t.Run("all-nodes returns unique sorted InternalIPs", func(t *testing.T) {
		t.Parallel()
		kube := fake.NewClientBuilder().WithScheme(controllerTestScheme(t)).WithObjects(
			routeGatewayNode("node-a",
				corev1.NodeAddress{Type: corev1.NodeHostName, Address: "node-a"},
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.0.2.11"},
			),
			routeGatewayNode("node-b",
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.0.2.10"},
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.0.2.99"},
			),
			routeGatewayNode("node-dup",
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.0.2.10"},
			),
			routeGatewayNode("node-host",
				corev1.NodeAddress{Type: corev1.NodeHostName, Address: "node-host"},
			),
		).Build()
		got, err := routeGateways(context.Background(), kube, routeGatewayService())
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"192.0.2.10", "192.0.2.11"}
		if len(got) != len(want) {
			t.Fatalf("gateways = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("gateways = %v, want %v", got, want)
			}
		}
	})

	t.Run("single-node returns exactly one InternalIP", func(t *testing.T) {
		t.Parallel()
		kube := fake.NewClientBuilder().WithScheme(controllerTestScheme(t)).WithObjects(
			routeGatewayNode("node-a",
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.0.2.11"},
			),
			routeGatewayNode("node-b",
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "192.0.2.10"},
			),
		).Build()
		service := routeGatewayService()
		service.Annotations = map[string]string{api.RouteModeAnnotation: "single-node"}
		got, err := routeGateways(context.Background(), kube, service)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("gateways = %v, want exactly one address", got)
		}
		if got[0] != "192.0.2.10" && got[0] != "192.0.2.11" {
			t.Fatalf("gateway = %q, want a node InternalIP", got[0])
		}
	})

	t.Run("no InternalIP is an error", func(t *testing.T) {
		t.Parallel()
		kube := fake.NewClientBuilder().WithScheme(controllerTestScheme(t)).WithObjects(
			routeGatewayNode("node-host",
				corev1.NodeAddress{Type: corev1.NodeHostName, Address: "node-host"},
			),
		).Build()
		_, err := routeGateways(context.Background(), kube, routeGatewayService())
		if err == nil || !strings.Contains(err.Error(), "no node InternalIP") {
			t.Fatalf("error = %v, want no node InternalIP", err)
		}
	})
}

func TestServiceWantsClusterRoute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		service corev1.Service
		want    bool
	}{
		{
			name: "cluster IP with DNS annotation",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{api.DNSNameAnnotation: "web.example.com"}},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
			},
			want: true,
		},
		{
			name: "empty type is treated as ClusterIP",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{api.DNSNameAnnotation: "web.example.com"}},
				Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.8"},
			},
			want: true,
		},
		{
			name: "missing DNS annotation",
			service: corev1.Service{
				Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
			},
		},
		{
			name: "headless cluster IP",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{api.DNSNameAnnotation: "web.example.com"}},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone},
			},
		},
		{
			name: "NodePort is not a cluster route candidate",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{api.DNSNameAnnotation: "web.example.com"}},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ClusterIP: "10.0.0.8"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := serviceWantsClusterRoute(test.service); got != test.want {
				t.Fatalf("serviceWantsClusterRoute() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestIsClusterIPService(t *testing.T) {
	t.Parallel()
	if !isClusterIPService(corev1.Service{}) {
		t.Fatal("empty type must count as ClusterIP")
	}
	if !isClusterIPService(corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP}}) {
		t.Fatal("ClusterIP type must count as ClusterIP")
	}
	if isClusterIPService(corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort}}) {
		t.Fatal("NodePort must not count as ClusterIP")
	}
}
