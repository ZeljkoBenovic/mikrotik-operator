package controller

import (
	"context"
	"errors"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
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

func TestIgnoreMissingRouterDistinguishesSecretFromRouter(t *testing.T) {
	t.Parallel()
	routerErr := apierrors.NewNotFound(api.GroupVersion.WithResource("mikrotikrouters").GroupResource(), "edge")
	secretErr := apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "credentials")
	if err := ignoreMissingRouter(routerErr); err != nil {
		t.Fatalf("router not found = %v, want nil", err)
	}
	if err := ignoreMissingRouter(secretErr); err == nil {
		t.Fatal("secret not found was treated as a missing router")
	}
	if err := ignoreMissingRouter(errors.New("dial timeout")); err == nil {
		t.Fatal("non-NotFound error was ignored")
	}
}
