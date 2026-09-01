package controller

import (
	"context"
	"errors"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestPortForwardsForServiceMapsMatchingRefs(t *testing.T) {
	scheme := controllerTestScheme(t)
	matching := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{ServiceRef: &api.NamespacedName{Namespace: "app", Name: "backend"}},
	}
	otherService := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{ServiceRef: &api.NamespacedName{Namespace: "app", Name: "db"}},
	}
	podForward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{PodRef: &api.NamespacedName{Namespace: "app", Name: "backend"}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&matching, &otherService, &podForward).Build()
	reconciler := PortForwardReconciler{Client: kube}
	requests := reconciler.portForwardsForService(context.Background(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
	})
	if len(requests) != 1 || requests[0].Name != "web" || requests[0].Namespace != "app" {
		t.Fatalf("service watch mapping = %#v, want web/app", requests)
	}
}

func TestPortForwardsForPodMapsMatchingRefs(t *testing.T) {
	scheme := controllerTestScheme(t)
	matching := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{PodRef: &api.NamespacedName{Namespace: "app", Name: "web-0"}},
	}
	otherPod := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{PodRef: &api.NamespacedName{Namespace: "app", Name: "db-0"}},
	}
	serviceForward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{ServiceRef: &api.NamespacedName{Namespace: "app", Name: "web-0"}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&matching, &otherPod, &serviceForward).Build()
	reconciler := PortForwardReconciler{Client: kube}
	requests := reconciler.portForwardsForPod(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"},
	})
	if len(requests) != 1 || requests[0].Name != "web" {
		t.Fatalf("pod watch mapping = %#v, want web", requests)
	}
}

func TestIngressesInNamespaceMapsSameNamespace(t *testing.T) {
	scheme := controllerTestScheme(t)
	local := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "app-ing", Namespace: "app"}}
	other := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "other-ing", Namespace: "other"}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&local, &other).Build()
	reconciler := IngressReconciler{Client: kube}
	requests := reconciler.ingressesInNamespace(context.Background(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"},
	})
	if len(requests) != 1 || requests[0].Name != "app-ing" {
		t.Fatalf("ingress watch mapping = %#v, want app-ing", requests)
	}
}

func TestHTTPRoutesForReferenceGrantMapsFromNamespace(t *testing.T) {
	scheme := controllerTestScheme(t)
	route := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	other := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other"}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&route, &other).Build()
	reconciler := HTTPRouteReconciler{Client: kube}
	grant := &gatewayv1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "grant", Namespace: "infra"},
		Spec: gatewayv1.ReferenceGrantSpec{
			From: []gatewayv1.ReferenceGrantFrom{{
				Group:     gatewayv1.Group(gatewayv1.GroupVersion.Group),
				Kind:      "HTTPRoute",
				Namespace: "app",
			}},
		},
	}
	requests := reconciler.httpRoutesForReferenceGrant(context.Background(), grant)
	if len(requests) != 1 || requests[0].NamespacedName != (types.NamespacedName{Namespace: "app", Name: "web"}) {
		t.Fatalf("reference grant mapping = %#v, want app/web", requests)
	}
	if requests := reconciler.httpRoutesForReferenceGrant(context.Background(), &corev1.Service{}); len(requests) != 0 {
		t.Fatalf("non-grant object mapped %d requests", len(requests))
	}
}

func TestHTTPRoutesForGatewayMapsParentRefs(t *testing.T) {
	scheme := controllerTestScheme(t)
	gatewayNS := gatewayv1.Namespace("infra")
	attached := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Name:      "edge",
					Namespace: &gatewayNS,
				}},
			},
		},
	}
	unrelated := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "app"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: "other"}},
			},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&attached, &unrelated).Build()
	reconciler := HTTPRouteReconciler{Client: kube}
	requests := reconciler.httpRoutesForGateway(context.Background(), &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "infra"},
	})
	if len(requests) != 1 || requests[0].Name != "web" {
		t.Fatalf("gateway mapping = %#v, want web", requests)
	}
}

func TestHTTPRoutesForGatewayClassMapsAttachedRoutes(t *testing.T) {
	scheme := controllerTestScheme(t)
	gateway := gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "infra"},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "mikrotik"},
	}
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "infra"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: "edge"}},
			},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gateway, &route).Build()
	reconciler := HTTPRouteReconciler{Client: kube}
	requests := reconciler.httpRoutesForGatewayClass(context.Background(), &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "mikrotik"},
	})
	if len(requests) != 1 || requests[0].Name != "web" {
		t.Fatalf("gateway class mapping = %#v, want web", requests)
	}
	if requests := reconciler.httpRoutesForGatewayClass(context.Background(), &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "other"},
	}); len(requests) != 0 {
		t.Fatalf("unrelated gateway class mapped %d requests", len(requests))
	}
}

func TestHTTPRoutesForNamespaceMapsLocalRoutes(t *testing.T) {
	scheme := controllerTestScheme(t)
	local := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	other := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other"}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&local, &other).Build()
	reconciler := HTTPRouteReconciler{Client: kube}
	requests := reconciler.httpRoutesForNamespace(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app"},
	})
	if len(requests) != 1 || requests[0].Name != "web" {
		t.Fatalf("namespace mapping = %#v, want web", requests)
	}
	if requests := reconciler.httpRoutesForNamespace(context.Background(), &corev1.Service{}); len(requests) != 0 {
		t.Fatalf("non-namespace object mapped %d requests", len(requests))
	}
}

func TestRouterFenceCancelsWaitersWhileHeld(t *testing.T) {
	registry := newRouterFenceRegistry()
	key := types.NamespacedName{Namespace: "app", Name: "router"}
	release, err := registry.acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = registry.acquire(ctx, key)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire error = %v, want context.Canceled", err)
	}
	release()

	release, err = registry.acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
