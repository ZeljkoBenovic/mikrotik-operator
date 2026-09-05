package controller

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestIsServiceBackend(t *testing.T) {
	serviceKind := gatewayv1.Kind("Service")
	podKind := gatewayv1.Kind("Pod")
	coreGroup := gatewayv1.Group("")
	otherGroup := gatewayv1.Group("apps")
	tests := []struct {
		name    string
		backend gatewayv1.HTTPBackendRef
		want    bool
	}{
		{name: "core service by name", backend: httpBackendRef("web", "", 80), want: true},
		{
			name: "explicit Service kind",
			backend: gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: &coreGroup, Kind: &serviceKind, Name: "web",
			}}},
			want: true,
		},
		{
			name: "empty name",
			backend: gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Kind: &serviceKind,
			}}},
		},
		{
			name: "non-service kind",
			backend: gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Kind: &podKind, Name: "web",
			}}},
		},
		{
			name: "non-core group",
			backend: gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: &otherGroup, Kind: &serviceKind, Name: "web",
			}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isServiceBackend(test.backend); got != test.want {
				t.Fatalf("isServiceBackend() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCrossNamespaceGatewayParentDoesNotRequireReferenceGrant(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	all := gatewayv1.NamespacesFromAll
	hostname := gatewayv1.Hostname("app.example.com")
	gatewayClass := gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: api.GatewayClassName},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: gatewayv1.GatewayController(api.GatewayController)},
	}
	gateway := gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "infra"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(api.GatewayClassName),
			Listeners: []gatewayv1.Listener{{
				Name: "https", Protocol: gatewayv1.HTTPSProtocolType, Port: 443,
				AllowedRoutes: &gatewayv1.AllowedRoutes{Namespaces: &gatewayv1.RouteNamespaces{From: &all}},
			}},
		},
	}
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "edge", Namespace: pointerTo(gatewayv1.Namespace("infra"))}}},
			Hostnames:       []gatewayv1.Hostname{hostname},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gatewayClass, &gateway).Build()
	reconciler := HTTPRouteReconciler{Client: kube}
	got, attached, err := reconciler.acceptedHostnamesForMikroTikGateway(context.Background(), route)
	if err != nil {
		t.Fatal(err)
	}
	if !attached || !slices.Equal(got, []gatewayv1.Hostname{hostname}) {
		t.Fatalf("got hostnames %v attached=%t", got, attached)
	}
}

func TestReferenceGrantPermitsCrossNamespaceService(t *testing.T) {
	route := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app"}}
	matchingFrom := gatewayv1.ReferenceGrantFrom{
		Group:     gatewayv1.Group(gatewayv1.GroupVersion.Group),
		Kind:      gatewayv1.Kind("HTTPRoute"),
		Namespace: gatewayv1.Namespace("app"),
	}
	matchingTo := gatewayv1.ReferenceGrantTo{Group: "", Kind: "Service", Name: pointerTo(gatewayv1.ObjectName("backend"))}
	tests := []struct {
		name  string
		grant gatewayv1.ReferenceGrant
		want  bool
	}{
		{name: "exact service", grant: referenceGrant("backend", matchingFrom, matchingTo), want: true},
		{name: "all service names", grant: referenceGrant("backend", matchingFrom, gatewayv1.ReferenceGrantTo{Group: "", Kind: "Service"}), want: true},
		{name: "wrong source namespace", grant: referenceGrant("backend", gatewayv1.ReferenceGrantFrom{Group: matchingFrom.Group, Kind: matchingFrom.Kind, Namespace: "other"}, matchingTo)},
		{name: "wrong source group", grant: referenceGrant("backend", gatewayv1.ReferenceGrantFrom{Group: "other.example", Kind: matchingFrom.Kind, Namespace: matchingFrom.Namespace}, matchingTo)},
		{name: "wrong target group", grant: referenceGrant("backend", matchingFrom, gatewayv1.ReferenceGrantTo{Group: gatewayv1.Group(gatewayv1.GroupVersion.Group), Kind: "Service", Name: matchingTo.Name})},
		{name: "wrong target kind", grant: referenceGrant("backend", matchingFrom, gatewayv1.ReferenceGrantTo{Group: "", Kind: "Gateway", Name: matchingTo.Name})},
		{name: "wrong target name", grant: referenceGrant("backend", matchingFrom, gatewayv1.ReferenceGrantTo{Group: "", Kind: "Service", Name: pointerTo(gatewayv1.ObjectName("other"))})},
		{name: "grant in wrong target namespace", grant: referenceGrant("other", matchingFrom, matchingTo)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := gatewayv1.Install(scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&test.grant).Build()
			got, err := referenceGrantPermitsService(context.Background(), kube, route, "backend", "backend")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}

func TestAcceptedListenerHostnamesHonorsParentPortAndUnionsListeners(t *testing.T) {
	foo := gatewayv1.Hostname("foo.example.com")
	bar := gatewayv1.Hostname("bar.example.com")
	gateway := gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
		Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{
			{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80, Hostname: &foo},
			{Name: "https", Protocol: gatewayv1.HTTPSProtocolType, Port: 443, Hostname: &bar},
			{Name: "tcp", Protocol: gatewayv1.TCPProtocolType, Port: 9000},
		}},
	}
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app"},
		Spec:       gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{foo, bar}},
	}
	tests := []struct {
		name     string
		parent   gatewayv1.ParentReference
		want     []gatewayv1.Hostname
		attached bool
	}{
		{name: "all matching listeners union", parent: gatewayv1.ParentReference{Name: "edge"}, want: []gatewayv1.Hostname{foo, bar}, attached: true},
		{name: "port selects listener", parent: gatewayv1.ParentReference{Name: "edge", Port: pointerTo(gatewayv1.PortNumber(443))}, want: []gatewayv1.Hostname{bar}, attached: true},
		{name: "unknown port", parent: gatewayv1.ParentReference{Name: "edge", Port: pointerTo(gatewayv1.PortNumber(8443))}},
		{name: "section and port must both match", parent: gatewayv1.ParentReference{Name: "edge", SectionName: pointerTo(gatewayv1.SectionName("http")), Port: pointerTo(gatewayv1.PortNumber(443))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, attached, err := acceptedListenerHostnames(context.Background(), nil, gateway, test.parent, route)
			if err != nil {
				t.Fatal(err)
			}
			sort.Slice(got, func(left, right int) bool { return got[left] < got[right] })
			sort.Slice(test.want, func(left, right int) bool { return test.want[left] < test.want[right] })
			if attached != test.attached || !slices.Equal(got, test.want) {
				t.Fatalf("got hostnames %v attached=%t, want %v attached=%t", got, attached, test.want, test.attached)
			}
		})
	}
}

func TestAcceptedListenerHostnamesHonorsAllowedRoutes(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")
	httpRouteKind := gatewayv1.RouteGroupKind{Kind: "HTTPRoute"}
	tcpRouteKind := gatewayv1.RouteGroupKind{Kind: "TCPRoute"}
	fromSame := gatewayv1.NamespacesFromSame
	fromSelector := gatewayv1.NamespacesFromSelector
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"team": "edge"}}
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app"},
		Spec:       gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{hostname}},
	}
	tests := []struct {
		name      string
		gatewayNS string
		listener  gatewayv1.Listener
		namespace *corev1.Namespace
		want      []gatewayv1.Hostname
		attached  bool
	}{
		{
			name:      "default same-namespace attaches",
			gatewayNS: "app",
			listener:  gatewayv1.Listener{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
			want:      []gatewayv1.Hostname{hostname},
			attached:  true,
		},
		{
			name:      "default rejects cross-namespace",
			gatewayNS: "infra",
			listener:  gatewayv1.Listener{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
		},
		{
			name:      "kinds omit HTTPRoute",
			gatewayNS: "app",
			listener: gatewayv1.Listener{
				Name:     "http",
				Protocol: gatewayv1.HTTPProtocolType,
				Port:     80,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: []gatewayv1.RouteGroupKind{tcpRouteKind},
				},
			},
		},
		{
			name:      "kinds allow HTTPRoute",
			gatewayNS: "app",
			listener: gatewayv1.Listener{
				Name:     "http",
				Protocol: gatewayv1.HTTPProtocolType,
				Port:     80,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Kinds: []gatewayv1.RouteGroupKind{httpRouteKind},
				},
			},
			want:     []gatewayv1.Hostname{hostname},
			attached: true,
		},
		{
			name:      "From=Same rejects other namespace",
			gatewayNS: "infra",
			listener: gatewayv1.Listener{
				Name:     "http",
				Protocol: gatewayv1.HTTPProtocolType,
				Port:     80,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{From: &fromSame},
				},
			},
		},
		{
			name:      "selector matches route namespace",
			gatewayNS: "infra",
			listener: gatewayv1.Listener{
				Name:     "http",
				Protocol: gatewayv1.HTTPProtocolType,
				Port:     80,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{From: &fromSelector, Selector: selector},
				},
			},
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app", Labels: map[string]string{"team": "edge"}}},
			want:      []gatewayv1.Hostname{hostname},
			attached:  true,
		},
		{
			name:      "selector rejects unlabeled namespace",
			gatewayNS: "infra",
			listener: gatewayv1.Listener{
				Name:     "http",
				Protocol: gatewayv1.HTTPProtocolType,
				Port:     80,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{From: &fromSelector, Selector: selector},
				},
			},
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app", Labels: map[string]string{"team": "other"}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if test.namespace != nil {
				builder = builder.WithObjects(test.namespace)
			}
			kube := builder.Build()
			gateway := gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: test.gatewayNS},
				Spec:       gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{test.listener}},
			}
			got, attached, err := acceptedListenerHostnames(context.Background(), kube, gateway, gatewayv1.ParentReference{Name: "edge"}, route)
			if err != nil {
				t.Fatal(err)
			}
			if attached != test.attached || !slices.Equal(got, test.want) {
				t.Fatalf("got hostnames %v attached=%t, want %v attached=%t", got, attached, test.want, test.attached)
			}
		})
	}
}

func TestAcceptedRouteHostnamesReturnsEffectiveIntersections(t *testing.T) {
	tests := []struct {
		name     string
		listener *gatewayv1.Hostname
		route    []gatewayv1.Hostname
		want     []gatewayv1.Hostname
	}{
		{name: "unconstrained listener", route: []gatewayv1.Hostname{"foo.example.com"}, want: []gatewayv1.Hostname{"foo.example.com"}},
		{name: "omitted route inherits listener", listener: pointerTo(gatewayv1.Hostname("foo.example.com")), want: []gatewayv1.Hostname{"foo.example.com"}},
		{name: "exact listener narrows route wildcard", listener: pointerTo(gatewayv1.Hostname("foo.example.com")), route: []gatewayv1.Hostname{"*.example.com"}, want: []gatewayv1.Hostname{"foo.example.com"}},
		{name: "exact route narrows listener wildcard", listener: pointerTo(gatewayv1.Hostname("*.example.com")), route: []gatewayv1.Hostname{"foo.example.com"}, want: []gatewayv1.Hostname{"foo.example.com"}},
		{name: "multi-label exact route matches wildcard", listener: pointerTo(gatewayv1.Hostname("*.example.com")), route: []gatewayv1.Hostname{"foo.bar.example.com"}, want: []gatewayv1.Hostname{"foo.bar.example.com"}},
		{name: "wildcard does not match bare suffix", listener: pointerTo(gatewayv1.Hostname("*.example.com")), route: []gatewayv1.Hostname{"example.com"}},
		{name: "equal wildcards remain wildcard", listener: pointerTo(gatewayv1.Hostname("*.example.com")), route: []gatewayv1.Hostname{"*.example.com"}, want: []gatewayv1.Hostname{"*.example.com"}},
		{name: "nested route wildcard is narrower", listener: pointerTo(gatewayv1.Hostname("*.example.com")), route: []gatewayv1.Hostname{"*.sub.example.com"}, want: []gatewayv1.Hostname{"*.sub.example.com"}},
		{name: "nested listener wildcard is narrower", listener: pointerTo(gatewayv1.Hostname("*.sub.example.com")), route: []gatewayv1.Hostname{"*.example.com"}, want: []gatewayv1.Hostname{"*.sub.example.com"}},
		{name: "disjoint wildcards", listener: pointerTo(gatewayv1.Hostname("*.example.com")), route: []gatewayv1.Hostname{"*.example.net"}},
		{name: "nonmatching exact names", listener: pointerTo(gatewayv1.Hostname("foo.example.com")), route: []gatewayv1.Hostname{"bar.example.com"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := acceptedRouteHostnames(test.listener, test.route)
			if !slices.Equal(got, test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestCleanupOwnedChildrenPreservesUnownedLabelCollisions(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	route := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app", UID: types.UID("route-uid")}}
	ownedRecord := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "owned-dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/httproute": "route"}}}
	ownedForward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "owned-pf", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}}}
	if err := controllerutil.SetControllerReference(&route, &ownedRecord, scheme); err != nil {
		t.Fatal(err)
	}
	if err := controllerutil.SetControllerReference(&route, &ownedForward, scheme); err != nil {
		t.Fatal(err)
	}
	unownedRecord := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "unowned-dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/httproute": "route"}}}
	unownedForward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "unowned-pf", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ownedRecord, &ownedForward, &unownedRecord, &unownedForward).Build()
	if err := cleanupOwnedChildren(context.Background(), kube, scheme, &route, "httproute", "route", "httproute/route"); err != nil {
		t.Fatal(err)
	}
	assertNotFound(t, kube, &api.MikroTikDNSRecord{}, "app", "owned-dns")
	assertNotFound(t, kube, &api.MikroTikPortForward{}, "app", "owned-pf")
	assertExists(t, kube, &api.MikroTikDNSRecord{}, "app", "unowned-dns")
	assertExists(t, kube, &api.MikroTikPortForward{}, "app", "unowned-pf")
}

func TestIngressMissingClassCleansOwnedChildren(t *testing.T) {
	scheme := controllerTestScheme(t)
	className := api.IngressClassName
	ingress := networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "app", UID: "ingress-uid"}, Spec: networkingv1.IngressSpec{IngressClassName: &className}}
	record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/ingress": ingress.Name}}}
	forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "forward", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/ingress/ingress")}}}
	if err := controllerutil.SetControllerReference(&ingress, &record, scheme); err != nil {
		t.Fatal(err)
	}
	if err := controllerutil.SetControllerReference(&ingress, &forward, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ingress, &record, &forward).Build()
	reconciler := IngressReconciler{Client: kube, RuntimeScheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "ingress")); !apierrors.IsNotFound(err) {
		t.Fatalf("expected missing IngressClass error, got %v", err)
	}
	assertNotFound(t, kube, &api.MikroTikDNSRecord{}, "app", "dns")
	assertNotFound(t, kube, &api.MikroTikPortForward{}, "app", "forward")
}

func TestIngressInvalidBackendPortStillPrunesOwnedForwards(t *testing.T) {
	scheme := controllerTestScheme(t)
	className := api.IngressClassName
	ingressClass := networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: className}, Spec: networkingv1.IngressClassSpec{Controller: api.IngressController}}
	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress",
			Namespace: "app",
			UID:       "ingress-uid",
			Annotations: map[string]string{
				api.PublicIPAnnotation:  "198.51.100.10",
				api.RouterRefAnnotation: "router",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: "invalid.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "backend",
									Port: networkingv1.ServiceBackendPort{Number: 81},
								},
							},
						}},
					},
				},
			}},
		},
	}
	service := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.10", Ports: []corev1.ServicePort{{Port: 80}, {Port: 9090}}}}
	record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "stale-dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/ingress": ingress.Name}}}
	forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "stale", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/ingress/ingress")}}}
	if err := controllerutil.SetControllerReference(&ingress, &record, scheme); err != nil {
		t.Fatal(err)
	}
	if err := controllerutil.SetControllerReference(&ingress, &forward, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ingressClass, &ingress, &service, &record, &forward).Build()
	reconciler := IngressReconciler{Client: kube, RuntimeScheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "ingress")); err == nil {
		t.Fatal("expected invalid backend port error")
	}
	assertNoOwnedGeneratedChildren(t, kube, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name)
}

func TestHTTPRouteInvalidBackendPortStillPrunesOwnedForwards(t *testing.T) {
	scheme := controllerTestScheme(t)
	gatewayClass := gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: api.GatewayClassName}, Spec: gatewayv1.GatewayClassSpec{ControllerName: api.GatewayController}}
	gateway := gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: api.GatewayClassName, Listeners: []gatewayv1.Listener{{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80}}},
	}
	port := gatewayv1.PortNumber(81)
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route",
			Namespace: "app",
			UID:       "route-uid",
			Annotations: map[string]string{
				api.PublicIPAnnotation:  "198.51.100.10",
				api.RouterRefAnnotation: "router",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "edge"}}},
			Hostnames:       []gatewayv1.Hostname{"invalid.example.com"},
			Rules:           []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "backend", Port: &port}}}}}},
		},
	}
	service := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.10", Ports: []corev1.ServicePort{{Port: 80}, {Port: 9090}}}}
	record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "stale-dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/httproute": route.Name}}}
	forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "stale", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}}}
	if err := controllerutil.SetControllerReference(&route, &record, scheme); err != nil {
		t.Fatal(err)
	}
	if err := controllerutil.SetControllerReference(&route, &forward, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gatewayClass, &gateway, &route, &service, &record, &forward).Build()
	reconciler := HTTPRouteReconciler{Client: kube, Scheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "route")); err == nil {
		t.Fatal("expected invalid backend port error")
	}
	assertNoOwnedGeneratedChildren(t, kube, &route, "httproute", route.Name, "httproute/"+route.Name)
}

func TestHTTPRouteObservationErrorsPreserveOwnedChildren(t *testing.T) {
	sentinel := errors.New("transient API read failure")
	tests := []struct {
		name       string
		failGet    string
		failGrants bool
	}{
		{name: "Gateway Get", failGet: "Gateway"},
		{name: "GatewayClass Get", failGet: "GatewayClass"},
		{name: "Namespace Get", failGet: "Namespace"},
		{name: "ReferenceGrant List", failGrants: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := controllerTestScheme(t)
			fromSelector := gatewayv1.NamespacesFromSelector
			listener := gatewayv1.Listener{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80}
			if test.failGet == "Namespace" {
				listener.AllowedRoutes = &gatewayv1.AllowedRoutes{Namespaces: &gatewayv1.RouteNamespaces{
					From:     &fromSelector,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"attach": "true"}},
				}}
			}
			gatewayClass := gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: api.GatewayClassName}, Spec: gatewayv1.GatewayClassSpec{ControllerName: api.GatewayController}}
			gateway := gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"}, Spec: gatewayv1.GatewaySpec{GatewayClassName: api.GatewayClassName, Listeners: []gatewayv1.Listener{listener}}}
			route := gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app", UID: "route-uid"},
				Spec:       gatewayv1.HTTPRouteSpec{CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "edge"}}}},
			}
			if test.failGrants {
				backendNamespace := gatewayv1.Namespace("backend")
				route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "service", Namespace: &backendNamespace}}}}}}
			}
			record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/httproute": route.Name}}}
			forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "forward", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}}}
			if err := controllerutil.SetControllerReference(&route, &record, scheme); err != nil {
				t.Fatal(err)
			}
			if err := controllerutil.SetControllerReference(&route, &forward, scheme); err != nil {
				t.Fatal(err)
			}
			base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gatewayClass, &gateway, &route, &record, &forward)
			kube := base.WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
					kind := object.GetObjectKind().GroupVersionKind().Kind
					if kind == "" {
						switch object.(type) {
						case *gatewayv1.Gateway:
							kind = "Gateway"
						case *gatewayv1.GatewayClass:
							kind = "GatewayClass"
						case *corev1.Namespace:
							kind = "Namespace"
						}
					}
					if test.failGet != "" && kind == test.failGet {
						return sentinel
					}
					return underlying.Get(ctx, key, object, options...)
				},
				List: func(ctx context.Context, underlying client.WithWatch, list client.ObjectList, options ...client.ListOption) error {
					if test.failGrants {
						if _, ok := list.(*gatewayv1.ReferenceGrantList); ok {
							return sentinel
						}
					}
					return underlying.List(ctx, list, options...)
				},
			}).Build()
			reconciler := HTTPRouteReconciler{Client: kube, Scheme: scheme}
			if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "route")); !errors.Is(err, sentinel) {
				t.Fatalf("got error %v, want transient error", err)
			}
			assertExists(t, kube, &api.MikroTikDNSRecord{}, "app", "dns")
			assertExists(t, kube, &api.MikroTikPortForward{}, "app", "forward")
		})
	}
}

func TestIngressTransientReadsPreserveOwnedChildren(t *testing.T) {
	sentinel := errors.New("transient API read failure")
	tests := []struct {
		name      string
		failClass bool
	}{
		{name: "IngressClass Get", failClass: true},
		{name: "backend Service Get"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := controllerTestScheme(t)
			className := api.IngressClassName
			ingressClass := networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: className}, Spec: networkingv1.IngressClassSpec{Controller: api.IngressController}}
			ingress := networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "app", UID: "ingress-uid"},
				Spec: networkingv1.IngressSpec{
					IngressClassName: &className,
					Rules: []networkingv1.IngressRule{{
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{{
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "backend",
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								}},
							},
						},
					}},
				},
			}
			record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/ingress": ingress.Name}}}
			forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "forward", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/ingress/ingress")}}}
			if err := controllerutil.SetControllerReference(&ingress, &record, scheme); err != nil {
				t.Fatal(err)
			}
			if err := controllerutil.SetControllerReference(&ingress, &forward, scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ingressClass, &ingress, &record, &forward).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
					if test.failClass {
						if _, ok := object.(*networkingv1.IngressClass); ok {
							return sentinel
						}
					} else if _, ok := object.(*corev1.Service); ok {
						return sentinel
					}
					return underlying.Get(ctx, key, object, options...)
				},
			}).Build()
			reconciler := IngressReconciler{Client: kube, RuntimeScheme: scheme}
			if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "ingress")); !errors.Is(err, sentinel) {
				t.Fatalf("got error %v, want transient error", err)
			}
			assertExists(t, kube, &api.MikroTikDNSRecord{}, "app", "dns")
			assertExists(t, kube, &api.MikroTikPortForward{}, "app", "forward")
		})
	}
}

func TestHTTPRouteTransientServiceGetPreservesOwnedChildren(t *testing.T) {
	sentinel := errors.New("transient Service read failure")
	scheme := controllerTestScheme(t)
	gatewayClass := gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: api.GatewayClassName}, Spec: gatewayv1.GatewayClassSpec{ControllerName: api.GatewayController}}
	gateway := gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"}, Spec: gatewayv1.GatewaySpec{GatewayClassName: api.GatewayClassName, Listeners: []gatewayv1.Listener{{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80}}}}
	port := gatewayv1.PortNumber(80)
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "app", UID: "route-uid"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "edge"}}},
			Rules:           []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "backend", Port: &port}}}}}},
		},
	}
	record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/httproute": route.Name}}}
	forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "forward", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}}}
	if err := controllerutil.SetControllerReference(&route, &record, scheme); err != nil {
		t.Fatal(err)
	}
	if err := controllerutil.SetControllerReference(&route, &forward, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gatewayClass, &gateway, &route, &record, &forward).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
			if _, ok := object.(*corev1.Service); ok {
				return sentinel
			}
			return underlying.Get(ctx, key, object, options...)
		},
	}).Build()
	reconciler := HTTPRouteReconciler{Client: kube, Scheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "route")); !errors.Is(err, sentinel) {
		t.Fatalf("got error %v, want transient error", err)
	}
	assertExists(t, kube, &api.MikroTikDNSRecord{}, "app", "dns")
	assertExists(t, kube, &api.MikroTikPortForward{}, "app", "forward")
}

func TestIngressAmbiguitiesAreRejectedBeforeChildCreation(t *testing.T) {
	tests := []struct {
		name       string
		hosts      [2]string
		ports      [2]int32
		serviceIPs [2]string
	}{
		{
			name:       "same public port targets different Services",
			hosts:      [2]string{"one.example.com", "two.example.com"},
			ports:      [2]int32{80, 80},
			serviceIPs: [2]string{"10.0.0.10", "10.0.0.20"},
		},
		{
			name:       "same hostname targets different paths",
			hosts:      [2]string{"shared.example.com", "shared.example.com"},
			ports:      [2]int32{80, 81},
			serviceIPs: [2]string{"10.0.0.10", "10.0.0.20"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := controllerTestScheme(t)
			className := api.IngressClassName
			ingressClass := networkingv1.IngressClass{
				ObjectMeta: metav1.ObjectMeta{Name: className},
				Spec:       networkingv1.IngressClassSpec{Controller: api.IngressController},
			}
			ingress := networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ingress",
					Namespace: "app",
					UID:       "ingress-uid",
					Annotations: map[string]string{
						api.PublicIPAnnotation:  "198.51.100.10",
						api.RouterRefAnnotation: "router",
					},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: &className,
					Rules: []networkingv1.IngressRule{
						ingressRuleForService(test.hosts[0], "one", test.ports[0]),
						ingressRuleForService(test.hosts[1], "two", test.ports[1]),
					},
				},
			}
			serviceOne := corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "app"},
				Spec: corev1.ServiceSpec{
					ClusterIP: test.serviceIPs[0],
					Ports:     []corev1.ServicePort{{Port: test.ports[0]}},
				},
			}
			serviceTwo := corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "app"},
				Spec: corev1.ServiceSpec{
					ClusterIP: test.serviceIPs[1],
					Ports:     []corev1.ServicePort{{Port: test.ports[1]}},
				},
			}
			record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "stale-dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/ingress": ingress.Name}}}
			forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "stale-pf", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/ingress/ingress")}}}
			if err := controllerutil.SetControllerReference(&ingress, &record, scheme); err != nil {
				t.Fatal(err)
			}
			if err := controllerutil.SetControllerReference(&ingress, &forward, scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ingressClass, &ingress, &serviceOne, &serviceTwo, &record, &forward).Build()
			reconciler := IngressReconciler{Client: kube, RuntimeScheme: scheme}
			if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "ingress")); !errors.Is(err, errGeneratedChildAmbiguity) {
				t.Fatalf("got error %v, want generated-child ambiguity", err)
			}
			assertNoOwnedGeneratedChildren(t, kube, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name)
		})
	}
}

func TestHTTPRouteAmbiguitiesAreRejectedBeforeChildCreation(t *testing.T) {
	tests := []struct {
		name  string
		ports [2]gatewayv1.PortNumber
	}{
		{name: "same public port targets different Services", ports: [2]gatewayv1.PortNumber{80, 80}},
		{name: "same hostname targets different rules", ports: [2]gatewayv1.PortNumber{80, 81}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := controllerTestScheme(t)
			gatewayClass, gateway := mikroTikGatewayFixture()
			route := gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "route",
					Namespace: "app",
					UID:       "route-uid",
					Annotations: map[string]string{
						api.PublicIPAnnotation:  "198.51.100.10",
						api.RouterRefAnnotation: "router",
					},
				},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "edge"}}},
					Hostnames:       []gatewayv1.Hostname{"shared.example.com"},
					Rules: []gatewayv1.HTTPRouteRule{
						{BackendRefs: []gatewayv1.HTTPBackendRef{httpBackendRef("one", "", test.ports[0])}},
						{BackendRefs: []gatewayv1.HTTPBackendRef{httpBackendRef("two", "", test.ports[1])}},
					},
				},
			}
			serviceOne := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "app"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.10", Ports: []corev1.ServicePort{{Port: int32(test.ports[0])}}}}
			serviceTwo := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "app"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.20", Ports: []corev1.ServicePort{{Port: int32(test.ports[1])}}}}
			record := api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "stale-dns", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/httproute": route.Name}}}
			forward := api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "stale-pf", Namespace: "app", Labels: map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}}}
			if err := controllerutil.SetControllerReference(&route, &record, scheme); err != nil {
				t.Fatal(err)
			}
			if err := controllerutil.SetControllerReference(&route, &forward, scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gatewayClass, &gateway, &route, &serviceOne, &serviceTwo, &record, &forward).Build()
			reconciler := HTTPRouteReconciler{Client: kube, Scheme: scheme}
			if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "route")); !errors.Is(err, errGeneratedChildAmbiguity) {
				t.Fatalf("got error %v, want generated-child ambiguity", err)
			}
			assertNoOwnedGeneratedChildren(t, kube, &route, "httproute", route.Name, "httproute/"+route.Name)
		})
	}
}

func TestHTTPRouteSameServiceNameAcrossNamespacesHasUniquePortForwardChildren(t *testing.T) {
	scheme := controllerTestScheme(t)
	gatewayClass, gateway := mikroTikGatewayFixture()
	otherNamespace := gatewayv1.Namespace("other")
	portOne := gatewayv1.PortNumber(80)
	portTwo := gatewayv1.PortNumber(81)
	route := gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route",
			Namespace: "app",
			UID:       "route-uid",
			Annotations: map[string]string{
				api.PublicIPAnnotation:  "198.51.100.10",
				api.RouterRefAnnotation: "router",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "edge"}}},
			Rules: []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{
				httpBackendRef("backend", "", portOne),
				httpBackendRef("backend", otherNamespace, portTwo),
			}}},
		},
	}
	serviceOne := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "app"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.10", Ports: []corev1.ServicePort{{Port: 80}}}}
	serviceTwo := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "other"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.20", Ports: []corev1.ServicePort{{Port: 81}}}}
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
	grant := referenceGrant("other", gatewayv1.ReferenceGrantFrom{
		Group:     gatewayv1.Group(gatewayv1.GroupVersion.Group),
		Kind:      gatewayv1.Kind("HTTPRoute"),
		Namespace: gatewayv1.Namespace("app"),
	}, gatewayv1.ReferenceGrantTo{Group: "", Kind: "Service", Name: pointerTo(gatewayv1.ObjectName("backend"))})
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&gatewayClass, &gateway, &route, &serviceOne, &serviceTwo, &router, &grant, &node).Build()
	reconciler := HTTPRouteReconciler{Client: kube, Scheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "route")); err != nil {
		t.Fatal(err)
	}
	var forwards api.MikroTikPortForwardList
	if err := kube.List(context.Background(), &forwards, client.InNamespace("app"), client.MatchingLabels{"mikrotik.operator.io/port-forward-source": shortHash("app/httproute/route")}); err != nil {
		t.Fatal(err)
	}
	if len(forwards.Items) != 2 {
		t.Fatalf("got %d generated port forwards, want 2", len(forwards.Items))
	}
	names := map[string]struct{}{}
	namespaces := map[string]struct{}{}
	for _, forward := range forwards.Items {
		names[forward.Name] = struct{}{}
		if forward.Spec.ServiceRef == nil {
			t.Fatalf("generated port forward %s has no ServiceRef", forward.Name)
		}
		namespaces[forward.Spec.ServiceRef.Namespace] = struct{}{}
	}
	if len(names) != 2 || len(namespaces) != 2 {
		t.Fatalf("generated identities collided: names=%v namespaces=%v", names, namespaces)
	}
}

func ingressRuleForService(host, service string, port int32) networkingv1.IngressRule {
	return networkingv1.IngressRule{
		Host: host,
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
				Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
					Name: service,
					Port: networkingv1.ServiceBackendPort{Number: port},
				}},
			}}},
		},
	}
}

func mikroTikGatewayFixture() (gatewayv1.GatewayClass, gatewayv1.Gateway) {
	return gatewayv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: api.GatewayClassName},
			Spec:       gatewayv1.GatewayClassSpec{ControllerName: api.GatewayController},
		}, gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: api.GatewayClassName,
				Listeners:        []gatewayv1.Listener{{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80}},
			},
		}
}

func httpBackendRef(name string, namespace gatewayv1.Namespace, port gatewayv1.PortNumber) gatewayv1.HTTPBackendRef {
	reference := gatewayv1.BackendObjectReference{Name: gatewayv1.ObjectName(name), Port: &port}
	if namespace != "" {
		reference.Namespace = &namespace
	}
	return gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{BackendObjectReference: reference}}
}

func assertNoOwnedGeneratedChildren(t *testing.T, kube client.Client, owner client.Object, dnsSourceKind, dnsSourceName, portForwardSource string) {
	t.Helper()
	var records api.MikroTikDNSRecordList
	if err := kube.List(context.Background(), &records, client.InNamespace(owner.GetNamespace()), client.MatchingLabels{"mikrotik.operator.io/" + dnsSourceKind: dnsSourceName}); err != nil {
		t.Fatal(err)
	}
	for _, record := range records.Items {
		if metav1.IsControlledBy(&record, owner) {
			t.Fatalf("owned DNS child %s survived ambiguity preflight", record.Name)
		}
	}
	var forwards api.MikroTikPortForwardList
	if err := kube.List(context.Background(), &forwards, client.InNamespace(owner.GetNamespace()), client.MatchingLabels{"mikrotik.operator.io/port-forward-source": shortHash(owner.GetNamespace() + "/" + portForwardSource)}); err != nil {
		t.Fatal(err)
	}
	for _, forward := range forwards.Items {
		if metav1.IsControlledBy(&forward, owner) {
			t.Fatalf("owned port-forward child %s survived ambiguity preflight", forward.Name)
		}
	}
	var routes api.MikroTikRouteList
	if err := kube.List(context.Background(), &routes, client.InNamespace(owner.GetNamespace())); err != nil {
		t.Fatal(err)
	}
	for _, route := range routes.Items {
		if metav1.IsControlledBy(&route, owner) {
			t.Fatalf("owned route child %s survived ambiguity preflight", route.Name)
		}
	}
}

func TestServiceRouteRouterRefsReturnsAllDistinctKnownRouters(t *testing.T) {
	service := corev1.Service{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{serviceRouteRouterAnnotation: "router-b"}}}
	record := api.MikroTikDNSRecord{
		Spec:   api.MikroTikDNSRecordSpec{RouterRef: "router-a"},
		Status: api.MikroTikDNSRecordStatus{RouterRef: "router-a"},
	}
	if got, want := serviceRouteRouterRefs(service, &record), []string{"router-b", "router-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func pointerTo[T any](value T) *T { return &value }

func reconcileUntil(t *testing.T, do func() error, done func() bool) {
	t.Helper()
	for i := 0; i < 8; i++ {
		if err := do(); err != nil {
			t.Fatalf("reconcile %d: %v", i+1, err)
		}
		if done() {
			return
		}
	}
	t.Fatal("reconcile did not converge")
}

func reconcileRequest(namespace, name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

func controllerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, install := range []func(*runtime.Scheme) error{api.AddToScheme, corev1.AddToScheme, networkingv1.AddToScheme, gatewayv1.Install} {
		if err := install(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func referenceGrant(namespace string, from gatewayv1.ReferenceGrantFrom, to gatewayv1.ReferenceGrantTo) gatewayv1.ReferenceGrant {
	return gatewayv1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "grant", Namespace: namespace},
		Spec:       gatewayv1.ReferenceGrantSpec{From: []gatewayv1.ReferenceGrantFrom{from}, To: []gatewayv1.ReferenceGrantTo{to}},
	}
}

func assertNotFound(t *testing.T, kube client.Client, object client.Object, namespace, name string) {
	t.Helper()
	err := kube.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, object)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected %s/%s to be absent, got %v", namespace, name, err)
	}
}

func assertExists(t *testing.T, kube client.Client, object client.Object, namespace, name string) {
	t.Helper()
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, object); err != nil {
		t.Fatalf("expected %s/%s to exist: %v", namespace, name, err)
	}
}
