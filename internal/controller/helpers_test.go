package controller

import (
	"context"
	"errors"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRouterEndpointsPrefersExplicitEndpoints(t *testing.T) {
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router"},
		Spec: api.MikroTikRouterSpec{
			Address: "legacy",
			Routers: []api.RouterEndpoint{{Name: "a", Address: "10.0.0.1"}},
		},
	}
	endpoints := routerEndpoints(router)
	if len(endpoints) != 1 || endpoints[0].Address != "10.0.0.1" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
}

func TestEndpointKeyIgnoresMetadataAndNormalizesDefaultPort(t *testing.T) {
	plainDefault := endpointKey(api.RouterEndpoint{
		Name:              "old-name",
		Address:           "10.0.0.1",
		CredentialsSecret: corev1.LocalObjectReference{Name: "old-credentials"},
	})
	plainExplicit := endpointKey(api.RouterEndpoint{
		Name:              "new-name",
		Address:           "10.0.0.1",
		Port:              8728,
		CredentialsSecret: corev1.LocalObjectReference{Name: "new-credentials"},
	})
	if plainDefault != plainExplicit {
		t.Fatalf("default and explicit API port differ: %q != %q", plainDefault, plainExplicit)
	}

	tlsDefault := endpointKey(api.RouterEndpoint{Address: "10.0.0.1", TLS: true})
	tlsExplicit := endpointKey(api.RouterEndpoint{Address: "10.0.0.1", Port: 8729, TLS: true})
	if tlsDefault != tlsExplicit {
		t.Fatalf("default and explicit TLS port differ: %q != %q", tlsDefault, tlsExplicit)
	}

	tests := []struct {
		name  string
		left  string
		right string
	}{
		{name: "DNS case and trailing dot", left: "Router.Example.COM.", right: "router.example.com"},
		{name: "equivalent IPv6 text", left: "2001:0db8:0:0:0:0:0:1", right: "2001:db8::1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := endpointKey(api.RouterEndpoint{Address: test.left})
			right := endpointKey(api.RouterEndpoint{Address: test.right, Port: 8728})
			if left != right {
				t.Fatalf("equivalent endpoint addresses differ: %q != %q", left, right)
			}
		})
	}
}

func TestRouterPersistsEndpointSnapshotBeforeExternalAccess(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app"},
		Spec: api.MikroTikRouterSpec{
			Address:           "192.0.2.10",
			CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
		},
	}
	factoryCalls := 0
	factory := func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		factoryCalls++
		return nil, errors.New("RouterOS must not be contacted")
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router).
		WithStatusSubresource(&api.MikroTikRouter{}).
		Build()
	reconciler := RouterReconciler{Client: kube, Scheme: scheme, Factory: factory}
	request := reconcile.Request{NamespacedName: types.NamespacedName{Name: router.Name, Namespace: router.Namespace}}

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var afterFinalizer api.MikroTikRouter
	if err := kube.Get(context.Background(), request.NamespacedName, &afterFinalizer); err != nil {
		t.Fatal(err)
	}
	if routerHasDurableCurrentEndpoints(afterFinalizer) {
		t.Fatal("router became writable before its endpoint snapshot was persisted")
	}
	if err := ensureRouterActive(context.Background(), kube, afterFinalizer); err == nil {
		t.Fatal("child write gate accepted a router without a durable endpoint snapshot")
	}

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var afterSnapshot api.MikroTikRouter
	if err := kube.Get(context.Background(), request.NamespacedName, &afterSnapshot); err != nil {
		t.Fatal(err)
	}
	if !routerHasDurableCurrentEndpoints(afterSnapshot) {
		t.Fatalf("current endpoint was not durably recorded: %#v", afterSnapshot.Status.AppliedEndpoints)
	}
	if factoryCalls != 0 {
		t.Fatalf("RouterOS was contacted %d times before snapshot persistence returned", factoryCalls)
	}
}

func TestRouterEndpointChangeStatusConflictBlocksNewEndpointWrites(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	endpointA := api.RouterEndpoint{Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
	endpointB := api.RouterEndpoint{Address: "192.0.2.20", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpointB}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpointA}},
	}
	conflict := apierrors.NewConflict(
		schema.GroupResource{Group: api.GroupVersion.Group, Resource: "mikrotikrouters"},
		router.Name,
		errors.New("concurrent status update"),
	)
	factoryCalls := 0
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router).
		WithStatusSubresource(&api.MikroTikRouter{}).
		Build()
	kube := interceptor.NewClient(base, interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, underlying client.Client, subresource string, object client.Object, options ...client.SubResourceUpdateOption) error {
			if subresource == "status" {
				return conflict
			}
			return underlying.SubResource(subresource).Update(context.Background(), object, options...)
		},
	})
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			factoryCalls++
			return nil, errors.New("RouterOS must not be contacted")
		},
	}
	request := reconcile.Request{NamespacedName: types.NamespacedName{Name: router.Name, Namespace: router.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), request); !apierrors.IsConflict(err) {
		t.Fatalf("got error %v, want status conflict", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("RouterOS was contacted %d times after the durable status update failed", factoryCalls)
	}
	var stored api.MikroTikRouter
	if err := base.Get(context.Background(), request.NamespacedName, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Status.AppliedEndpoints) != 1 || endpointKey(stored.Status.AppliedEndpoints[0]) != endpointKey(endpointA) {
		t.Fatalf("old cleanup endpoint was lost after conflict: %#v", stored.Status.AppliedEndpoints)
	}
	if routerHasDurableCurrentEndpoints(stored) {
		t.Fatal("new endpoint appeared durable after the status update failed")
	}
	if err := ensureRouterActive(context.Background(), base, stored); err == nil {
		t.Fatal("child write gate accepted the unrecorded replacement endpoint")
	}
}

func TestRouterEndpointChangePersistsUnionBeforeExternalCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	endpointA := api.RouterEndpoint{Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
	endpointB := api.RouterEndpoint{Address: "192.0.2.20", CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpointB}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpointA}},
	}
	factoryCalls := 0
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router).
		WithStatusSubresource(&api.MikroTikRouter{}).
		Build()
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			factoryCalls++
			return nil, errors.New("RouterOS must not be contacted")
		},
	}
	request := reconcile.Request{NamespacedName: types.NamespacedName{Name: router.Name, Namespace: router.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 0 {
		t.Fatalf("RouterOS was contacted %d times before endpoint history persistence returned", factoryCalls)
	}
	var stored api.MikroTikRouter
	if err := kube.Get(context.Background(), request.NamespacedName, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Status.AppliedEndpoints) != 2 ||
		endpointKey(stored.Status.AppliedEndpoints[0]) != endpointKey(endpointA) ||
		endpointKey(stored.Status.AppliedEndpoints[1]) != endpointKey(endpointB) {
		t.Fatalf("endpoint transition history is not durable: %#v", stored.Status.AppliedEndpoints)
	}
	if err := ensureRouterActive(context.Background(), kube, stored); err == nil {
		t.Fatal("child write gate accepted a Router while obsolete endpoint cleanup was still pending")
	}
}

func TestDuplicateRouterEndpointOwnershipIsDeterministic(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	winner := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{{
			Name: "renamed", Address: "10.0.0.1", CredentialsSecret: corev1.LocalObjectReference{Name: "first"},
		}}},
	}
	loser := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "network"},
		Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{{
			Address: "10.0.0.1", Port: 8728, CredentialsSecret: corev1.LocalObjectReference{Name: "rotated"},
		}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&winner, &loser).Build()
	if err := ensureRouterEndpointOwnership(context.Background(), kube, winner); err != nil {
		t.Fatalf("canonical owner rejected: %v", err)
	}
	if err := ensureRouterEndpointOwnership(context.Background(), kube, loser); err == nil {
		t.Fatal("duplicate non-canonical owner was accepted")
	}
	claimed, err := endpointClaimedByOtherRouter(context.Background(), kube, winner, routerEndpoints(winner)[0])
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("destructive sweep did not detect the sibling endpoint claim")
	}
}

func TestPersistDurableRouterTargetRetainsTransitionHistory(t *testing.T) {
	tests := []struct {
		name   string
		object client.Object
	}{
		{name: "dns", object: &api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "object", Namespace: "app", Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"}}}},
		{name: "route", object: &api.MikroTikRoute{ObjectMeta: metav1.ObjectMeta{Name: "object", Namespace: "app", Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"}}}},
		{name: "firewall", object: &api.MikroTikFirewallRule{ObjectMeta: metav1.ObjectMeta{Name: "object", Namespace: "app", Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"}}}},
		{name: "port-forward", object: &api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: "object", Namespace: "app", Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := api.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(test.object).Build()
			updated, err := persistDurableRouterTarget(context.Background(), kube, test.object, "router-b")
			if err != nil {
				t.Fatal(err)
			}
			if !updated {
				t.Fatal("transition target was not persisted before apply")
			}
			refs := durableRouterTargets(test.object)
			if len(refs) != 2 || refs[0] != "router-a" || refs[1] != "router-b" {
				t.Fatalf("unexpected durable targets: %v", refs)
			}
		})
	}
}

func TestPersistServiceRouteRouterTargetKeepsMissingChildHistory(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	service := corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "backend", Namespace: "app",
		Annotations: map[string]string{serviceRouteRouterAnnotation: "router-a"},
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service).Build()
	updated, err := persistServiceRouteRouterTarget(context.Background(), kube, &service, "router-b")
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("new router target was not persisted")
	}
	refs := serviceRouteRouterRefs(service, nil)
	if len(refs) != 2 || refs[0] != "router-a" || refs[1] != "router-b" {
		t.Fatalf("unexpected service router history: %v", refs)
	}
}

func TestCompactDurableRouterTargetReplacesAndClearsAnnotation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	object := &api.MikroTikFirewallRule{ObjectMeta: metav1.ObjectMeta{
		Name:        "object",
		Namespace:   "app",
		Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a,router-b"},
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(object).Build()

	updated, err := compactDurableRouterTarget(context.Background(), kube, object, "router-b")
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("compact to a single target did not persist")
	}
	if object.GetAnnotations()[durableRouterTargetsAnnotation] != "router-b" {
		t.Fatalf("annotation = %q, want router-b", object.GetAnnotations()[durableRouterTargetsAnnotation])
	}

	updated, err = compactDurableRouterTarget(context.Background(), kube, object, "router-b")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("identical target was written again")
	}

	updated, err = compactDurableRouterTarget(context.Background(), kube, object, "")
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("clearing durable targets did not persist")
	}
	if _, exists := object.GetAnnotations()[durableRouterTargetsAnnotation]; exists {
		t.Fatalf("annotation still present: %q", object.GetAnnotations()[durableRouterTargetsAnnotation])
	}
}

func TestReadyConditionPreservesTransitionTime(t *testing.T) {
	previous := metav1.NewTime(metav1.Now().Add(-60 * 1000000000))
	existing := []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Applied",
		Message:            "done",
		LastTransitionTime: previous,
	}}
	condition := readyCondition(existing, metav1.ConditionTrue, "Applied", "done")
	if !condition[0].LastTransitionTime.Equal(&previous) {
		t.Fatalf("transition time changed: got %v want %v", condition[0].LastTransitionTime, previous)
	}
}

func TestReadyConditionChangesTransitionTimeWhenStateChanges(t *testing.T) {
	existing := []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Failed", Message: "old"}}
	condition := readyCondition(existing, metav1.ConditionTrue, "Applied", "new")
	if condition[0].LastTransitionTime.IsZero() || condition[0].Status != metav1.ConditionTrue {
		t.Fatalf("condition was not updated: %#v", condition[0])
	}
}

func TestServiceAddress(t *testing.T) {
	scheme := controllerTestScheme(t)
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeHostName, Address: "node-a"},
			{Type: corev1.NodeInternalIP, Address: "192.0.2.10"},
		}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&node).Build()
	tests := []struct {
		name    string
		service corev1.Service
		want    string
		wantErr bool
	}{
		{
			name: "cluster IP",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8"},
			},
			want: "10.0.0.8",
		},
		{
			name: "headless cluster IP",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone},
			},
			wantErr: true,
		},
		{
			name: "missing cluster IP",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
			},
			wantErr: true,
		},
		{
			name: "node port uses node InternalIP",
			service: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ClusterIP: "10.0.0.8"},
			},
			want: "192.0.2.10",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := serviceAddress(context.Background(), kube, test.service)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !errors.Is(err, errServiceNotAddressable) {
					t.Fatalf("error = %v, want %v", err, errServiceNotAddressable)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("serviceAddress() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServiceAddressRejectsNodePortWithoutInternalIP(t *testing.T) {
	scheme := controllerTestScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ClusterIP: "10.0.0.8"},
	}
	_, err := serviceAddress(context.Background(), kube, service)
	if !errors.Is(err, errServiceNotAddressable) {
		t.Fatalf("error = %v, want %v", err, errServiceNotAddressable)
	}
}

func TestAppendUniqueServicePort(t *testing.T) {
	http := corev1.ServicePort{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}
	https := corev1.ServicePort{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP}
	got := appendUniqueServicePort(nil, http)
	got = appendUniqueServicePort(got, http)
	got = appendUniqueServicePort(got, https)
	if len(got) != 2 || got[0] != http || got[1] != https {
		t.Fatalf("ports = %#v, want [http https]", got)
	}
}

func TestPortForwardDestinationAddress(t *testing.T) {
	tests := []struct {
		name  string
		spec  string
		annot string
		want  string
	}{
		{name: "spec only", spec: "203.0.113.10", want: "203.0.113.10"},
		{name: "annotation fallback", annot: "198.51.100.10", want: "198.51.100.10"},
		{name: "spec preferred over annotation", spec: "203.0.113.10", annot: "198.51.100.10", want: "203.0.113.10"},
		{name: "empty", want: ""},
		{name: "spec whitespace ignored", spec: "  203.0.113.10  ", want: "203.0.113.10"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forward := api.MikroTikPortForward{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
				Spec:       api.MikroTikPortForwardSpec{DestinationAddress: test.spec},
			}
			if test.annot != "" {
				forward.Annotations[api.PublicIPAnnotation] = test.annot
			}
			if got := portForwardDestinationAddress(forward); got != test.want {
				t.Fatalf("portForwardDestinationAddress() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateRouterEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		router  api.MikroTikRouter
		wantErr string
	}{
		{
			name:    "empty spec",
			router:  api.MikroTikRouter{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"}},
			wantErr: "requires a legacy address and credentialsSecret",
		},
		{
			name: "legacy address without credentials",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec:       api.MikroTikRouterSpec{Address: "192.0.2.10"},
			},
			wantErr: "requires a legacy address and credentialsSecret",
		},
		{
			name: "routers entry with empty address",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{{
					CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
				}}},
			},
			wantErr: "endpoint 0 has an empty address",
		},
		{
			name: "routers entry with empty credentials",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{{
					Address: "192.0.2.10",
				}}},
			},
			wantErr: "endpoint 0 has an empty credentialsSecret",
		},
		{
			name: "valid routers entry",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{{
					Address:           "192.0.2.10",
					CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
				}}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRouterEndpoints(test.router)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestRouterEndpointsRequiresAddressAndCredentials(t *testing.T) {
	if endpoints := routerEndpoints(api.MikroTikRouter{
		Spec: api.MikroTikRouterSpec{Address: "192.0.2.10"},
	}); endpoints != nil {
		t.Fatalf("address without credentials = %#v, want nil", endpoints)
	}
	if endpoints := routerEndpoints(api.MikroTikRouter{
		Spec: api.MikroTikRouterSpec{CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"}},
	}); endpoints != nil {
		t.Fatalf("credentials without address = %#v, want nil", endpoints)
	}
}

func TestRouterCleanupEndpointsPrefersAppliedAndSkipsIncomplete(t *testing.T) {
	applied := api.RouterEndpoint{
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	incomplete := api.RouterEndpoint{Address: "192.0.2.11"}
	current := api.RouterEndpoint{
		Address:           "192.0.2.20",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	got := routerCleanupEndpoints(api.MikroTikRouter{
		Spec:   api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{current}},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{applied, incomplete}},
	})
	if len(got) != 1 || endpointKey(got[0]) != endpointKey(applied) {
		t.Fatalf("cleanup endpoints = %#v, want applied complete endpoint", got)
	}

	fallback := routerCleanupEndpoints(api.MikroTikRouter{
		Spec: api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{current}},
	})
	if len(fallback) != 1 || endpointKey(fallback[0]) != endpointKey(current) {
		t.Fatalf("empty applied history = %#v, want current spec endpoint", fallback)
	}
}

func TestDurableRouterEndpointUnionRotatesCredentialsAndKeepsRemoved(t *testing.T) {
	previous := []api.RouterEndpoint{
		{Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "old"}},
		{Address: "192.0.2.11", CredentialsSecret: corev1.LocalObjectReference{Name: "old"}},
	}
	current := []api.RouterEndpoint{{
		Name:              "renamed",
		Address:           "192.0.2.10",
		Port:              8728,
		CredentialsSecret: corev1.LocalObjectReference{Name: "rotated"},
	}}
	union := durableRouterEndpointUnion(previous, current)
	if len(union) != 2 {
		t.Fatalf("union length = %d, want 2", len(union))
	}
	if endpointKey(union[0]) != endpointKey(current[0]) {
		t.Fatalf("kept endpoint identity = %#v, want current physical endpoint", union[0])
	}
	if union[0].CredentialsSecret.Name != "rotated" {
		t.Fatalf("union kept stale credentials %q", union[0].CredentialsSecret.Name)
	}
	if endpointKey(union[1]) != endpointKey(previous[1]) {
		t.Fatalf("removed endpoint was dropped: %#v", union)
	}

	ipv6Previous := []api.RouterEndpoint{{Address: "2001:0db8:0:0:0:0:0:1"}}
	ipv6Current := []api.RouterEndpoint{{Address: "2001:db8::1", Port: 8728}}
	collapsed := durableRouterEndpointUnion(ipv6Previous, ipv6Current)
	if len(collapsed) != 1 || collapsed[0].Address != "2001:db8::1" {
		t.Fatalf("equivalent IPv6 endpoints were not collapsed: %#v", collapsed)
	}
}

func TestAppendUniqueService(t *testing.T) {
	web := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	otherNS := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "other"}}
	db := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "app"}}
	got := appendUniqueService(nil, web)
	got = appendUniqueService(got, web)
	got = appendUniqueService(got, otherNS)
	got = appendUniqueService(got, db)
	if len(got) != 3 || got[0].Namespace != "app" || got[1].Namespace != "other" || got[2].Name != "db" {
		t.Fatalf("services = %#v, want web/app, web/other, db/app", got)
	}
}

func TestFindServicePort(t *testing.T) {
	service := corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
		{Name: "http", Port: 80},
		{Name: "https", Port: 443},
	}}}
	got, ok := findServicePort(service, 443)
	if !ok || got.Name != "https" {
		t.Fatalf("findServicePort(443) = %#v ok=%t, want https", got, ok)
	}
	if _, ok := findServicePort(service, 22); ok {
		t.Fatal("findServicePort(22) unexpectedly matched")
	}
}

func TestFindIngressServicePortPrefersNameThenNumber(t *testing.T) {
	service := corev1.Service{Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
		{Name: "http", Port: 80},
		{Name: "https", Port: 443},
	}}}
	byName, ok := findIngressServicePort(service, networkingv1.ServiceBackendPort{Name: "https", Number: 80})
	if !ok || byName.Port != 443 {
		t.Fatalf("named port = %#v ok=%t, want https/443 even when number disagrees", byName, ok)
	}
	byNumber, ok := findIngressServicePort(service, networkingv1.ServiceBackendPort{Number: 80})
	if !ok || byNumber.Name != "http" {
		t.Fatalf("numeric port = %#v ok=%t, want http/80", byNumber, ok)
	}
	if _, ok := findIngressServicePort(service, networkingv1.ServiceBackendPort{Name: "dns"}); ok {
		t.Fatal("missing named port unexpectedly matched")
	}
}

func TestNormalizeGeneratedHostnameAndPublicIP(t *testing.T) {
	if got := normalizeGeneratedHostname(" WWW.Example.COM. "); got != "www.example.com" {
		t.Fatalf("hostname = %q, want www.example.com", got)
	}
	if got := normalizePublicIP(" 2001:0db8:0:0:0:0:0:1 "); got != "2001:db8::1" {
		t.Fatalf("public IP = %q, want compressed IPv6", got)
	}
	if got := normalizePublicIP(" not-an-ip "); got != "not-an-ip" {
		t.Fatalf("non-IP public IP = %q, want trimmed original", got)
	}
}

func TestRouterReconcilerIgnoresMissingObject(t *testing.T) {
	scheme := controllerTestScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := RouterReconciler{Client: kube}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "missing")); err != nil {
		t.Fatal(err)
	}
}
