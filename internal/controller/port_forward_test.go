package controller

import (
	"context"
	"strings"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestPortForwardReconcilerMovesLeftoverWhenRouterRefChanges(t *testing.T) {
	scheme, objects, factory, clients := activeExternalCleanupFixture(t)
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:     "router-b",
			Protocol:      "tcp",
			ExternalPort:  443,
			TargetPort:    8443,
			TargetAddress: "10.0.0.20",
		},
		Status: api.MikroTikPortForwardStatus{RouterRef: "router-a", Applied: true, TargetAddress: "10.0.0.20"},
	}
	objects = append(objects, &forward)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&forward).Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: factory}
	reconcileUntil(t, func() error {
		_, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name))
		return err
	}, func() bool {
		var stored api.MikroTikPortForward
		if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
			t.Fatal(err)
		}
		return stored.Status.Applied && stored.Status.RouterRef == "router-b"
	})
	if clients["router-a"].deletedForwards == 0 || clients["router-a"].deletedFirewall == 0 {
		t.Fatalf("router-a leftover NAT/firewall not deleted: forwards=%d firewall=%d", clients["router-a"].deletedForwards, clients["router-a"].deletedFirewall)
	}
	if clients["router-b"].deletedForwards != 0 || clients["router-b"].deletedFirewall != 0 {
		t.Fatal("router-b was cleaned unexpectedly")
	}
	if clients["router-a"].ensuredForwards != 0 {
		t.Fatal("router-a received apply after move")
	}
	if clients["router-b"].ensuredForwards == 0 || clients["router-b"].ensuredFirewall == 0 {
		t.Fatal("router-b did not receive the moved port-forward")
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Annotations[durableRouterTargetsAnnotation] != "router-b" {
		t.Fatalf("durable router annotation = %q, want router-b", stored.Annotations[durableRouterTargetsAnnotation])
	}
}

func TestPortForwardReconcilerAppliesPodIP(t *testing.T) {
	scheme := controllerTestScheme(t)
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
		ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"},
		Status:     corev1.PodStatus{PodIP: "10.42.0.17"},
	}
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:    router.Name,
			Protocol:     "tcp",
			ExternalPort: 80,
			TargetPort:   8080,
			PodRef:       &api.NamespacedName{Namespace: "app", Name: "web-0"},
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &pod, &forward).
		WithStatusSubresource(&router, &forward).
		Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredPortForwards) == 0 {
		t.Fatal("expected dst-nat apply from Pod IP")
	}
	got := routerClient.ensuredPortForwards[len(routerClient.ensuredPortForwards)-1]
	if got.Target != "10.42.0.17" || got.TargetPort != 8080 {
		t.Fatalf("pod dst-nat target = %#v, want 10.42.0.17:8080", got)
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.TargetAddress != "10.42.0.17" || !stored.Status.Applied {
		t.Fatalf("status = %#v, want applied pod IP", stored.Status)
	}
}

func TestPortForwardReconcilerCleansDurableTargetWhenServiceIsMissing(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a,router-b"},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:    "router-a",
			Protocol:     "tcp",
			ExternalPort: 80,
			TargetPort:   80,
			ServiceRef:   &api.NamespacedName{Namespace: "app", Name: "missing"},
		},
		Status: api.MikroTikPortForwardStatus{RouterRef: "router-a", Applied: true, TargetAddress: "10.0.0.20"},
	}
	objects = append(objects, &forward)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&forward).Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); err != nil {
		t.Fatal(err)
	}
	for name, routerClient := range clients {
		if routerClient.deletedForwards == 0 || routerClient.deletedFirewall == 0 {
			t.Fatalf("%s leftover NAT/firewall not deleted after Service disappearance: forwards=%d firewall=%d", name, routerClient.deletedForwards, routerClient.deletedFirewall)
		}
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Applied {
		t.Fatal("missing Service left the port-forward marked applied")
	}
}

func TestPortForwardReconcilerCleansDurableTargetWhenPodIsMissing(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:    "router-a",
			Protocol:     "tcp",
			ExternalPort: 80,
			TargetPort:   80,
			PodRef:       &api.NamespacedName{Namespace: "app", Name: "missing"},
		},
		Status: api.MikroTikPortForwardStatus{RouterRef: "router-a", Applied: true, TargetAddress: "10.42.0.17"},
	}
	objects = append(objects, &forward)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&forward).Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); err != nil {
		t.Fatal(err)
	}
	if clients["router-a"].deletedForwards == 0 || clients["router-a"].deletedFirewall == 0 {
		t.Fatalf("leftover NAT/firewall not deleted after Pod disappearance: forwards=%d firewall=%d", clients["router-a"].deletedForwards, clients["router-a"].deletedFirewall)
	}
	if clients["router-b"].deletedForwards != 0 {
		t.Fatal("missing Pod cleaned a router that was not in durable history")
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Applied {
		t.Fatal("missing Pod left the port-forward marked applied")
	}
}

func TestPortForwardReconcilerAppliesSpecDestinationAddress(t *testing.T) {
	scheme := controllerTestScheme(t)
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
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:          router.Name,
			Protocol:           "tcp",
			ExternalPort:       443,
			TargetPort:         8443,
			TargetAddress:      "10.0.0.20",
			DestinationAddress: "203.0.113.10",
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &forward).
		WithStatusSubresource(&router, &forward).
		Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredPortForwards) == 0 {
		t.Fatal("expected dst-nat apply")
	}
	got := routerClient.ensuredPortForwards[len(routerClient.ensuredPortForwards)-1]
	if got.PublicIP != "203.0.113.10" {
		t.Fatalf("dst-nat dst-address = %q, want 203.0.113.10", got.PublicIP)
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.ExternalAddress != "203.0.113.10" {
		t.Fatalf("status.externalAddress = %q, want 203.0.113.10", stored.Status.ExternalAddress)
	}
}

func TestPortForwardReconcilerPrefersSpecOverPublicIPAnnotation(t *testing.T) {
	scheme := controllerTestScheme(t)
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
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "app",
			Finalizers: []string{resourceFinalizer},
			Annotations: map[string]string{
				durableRouterTargetsAnnotation: router.Name,
				api.PublicIPAnnotation:         "198.51.100.10",
			},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:          router.Name,
			Protocol:           "tcp",
			ExternalPort:       80,
			TargetPort:         80,
			TargetAddress:      "10.0.0.20",
			DestinationAddress: "203.0.113.10",
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &forward).
		WithStatusSubresource(&router, &forward).
		Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredPortForwards) == 0 {
		t.Fatal("expected dst-nat apply")
	}
	got := routerClient.ensuredPortForwards[len(routerClient.ensuredPortForwards)-1]
	if got.PublicIP != "203.0.113.10" {
		t.Fatalf("dst-nat dst-address = %q, want spec destinationAddress", got.PublicIP)
	}
}

func TestPortForwardReconcilerRejectsInvalidDestinationAddress(t *testing.T) {
	scheme := controllerTestScheme(t)
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
	forward := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:          router.Name,
			Protocol:           "tcp",
			ExternalPort:       80,
			TargetPort:         80,
			TargetAddress:      "10.0.0.20",
			DestinationAddress: "not-an-ip",
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &forward).
		WithStatusSubresource(&router, &forward).
		Build()
	reconciler := PortForwardReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(forward.Namespace, forward.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.ensuredForwards != 0 {
		t.Fatalf("invalid destination address applied dst-nat %d times", routerClient.ensuredForwards)
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Applied {
		t.Fatal("invalid destination address marked applied")
	}
	if !strings.Contains(stored.Status.Conditions[0].Message, "destination address") {
		t.Fatalf("status message = %q, want destination address error", stored.Status.Conditions[0].Message)
	}
}

func TestReconcileServicePortForwardsSetsDestinationAddress(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "svc-uid"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service).Build()
	if err := reconcileServicePortForwards(context.Background(), portForwardReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      &service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
		publicIP:   "198.51.100.10",
		routerRef:  "home-router",
		services:   []corev1.Service{service},
	}); err != nil {
		t.Fatal(err)
	}
	var forwards api.MikroTikPortForwardList
	if err := kube.List(context.Background(), &forwards, client.InNamespace(service.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(forwards.Items) != 1 {
		t.Fatalf("generated port forwards = %d, want 1", len(forwards.Items))
	}
	got := forwards.Items[0]
	if got.Spec.DestinationAddress != "198.51.100.10" {
		t.Fatalf("spec.destinationAddress = %q, want 198.51.100.10", got.Spec.DestinationAddress)
	}
	if got.Annotations[api.PublicIPAnnotation] != "198.51.100.10" {
		t.Fatalf("public-ip annotation = %q, want 198.51.100.10", got.Annotations[api.PublicIPAnnotation])
	}
	if !metav1.IsControlledBy(&got, &service) {
		t.Fatal("generated port forward is not owned by the Service")
	}
}

func TestReconcileServicePortForwardsUpdatesDestinationAddress(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "svc-uid"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	existing := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pf-" + shortHash("app/service/web/app/web/80/tcp"),
			Namespace: "app",
			Labels:    map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/service/web")},
			Annotations: map[string]string{
				api.PublicIPAnnotation: "198.51.100.10",
			},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:     "home-router",
			Protocol:      "tcp",
			ExternalPort:  80,
			TargetPort:    80,
			TargetAddress: "",
			ServiceRef:    &api.NamespacedName{Namespace: "app", Name: "web"},
		},
	}
	if err := controllerutil.SetControllerReference(&service, &existing, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &existing).Build()
	if err := reconcileServicePortForwards(context.Background(), portForwardReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      &service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
		publicIP:   "198.51.100.10",
		routerRef:  "home-router",
		services:   []corev1.Service{service},
	}); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: existing.Namespace, Name: existing.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Spec.DestinationAddress != "198.51.100.10" {
		t.Fatalf("updated spec.destinationAddress = %q, want 198.51.100.10", stored.Spec.DestinationAddress)
	}
}
