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

func TestReconcileServicePortForwardsLeavesNodePortTargetAddressEmpty(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "svc-uid"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP, NodePort: 30080}},
		},
	}
	nodeHigh := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-z"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.20"}}},
	}
	nodeLow := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &nodeHigh, &nodeLow).Build()
	if err := reconcileServicePortForwards(context.Background(), portForwardReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      &service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
		publicIP:   "198.51.100.11",
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
	if got.Spec.TargetAddress != "" {
		t.Fatalf("spec.targetAddress = %q, want empty so the CR reconciler resolves the live node IP", got.Spec.TargetAddress)
	}
	if got.Spec.TargetPort != 30080 {
		t.Fatalf("spec.targetPort = %d, want NodePort 30080", got.Spec.TargetPort)
	}
}

func TestReconcileServicePortForwardsClearsBakedNodePortTarget(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "svc-uid"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP, NodePort: 30080}},
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	existing := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pf-" + shortHash("app/service/web/app/web/80/tcp"),
			Namespace: "app",
			Labels:    map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/service/web")},
			Annotations: map[string]string{
				api.PublicIPAnnotation: "198.51.100.11",
			},
		},
		Spec: api.MikroTikPortForwardSpec{
			RouterRef:          "home-router",
			Protocol:           "tcp",
			ExternalPort:       80,
			TargetPort:         30080,
			TargetAddress:      "192.0.2.20",
			DestinationAddress: "198.51.100.11",
			ServiceRef:         &api.NamespacedName{Namespace: "app", Name: "web"},
		},
	}
	if err := controllerutil.SetControllerReference(&service, &existing, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &node, &existing).Build()
	if err := reconcileServicePortForwards(context.Background(), portForwardReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      &service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
		publicIP:   "198.51.100.11",
		routerRef:  "home-router",
		services:   []corev1.Service{service},
	}); err != nil {
		t.Fatal(err)
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: existing.Namespace, Name: existing.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Spec.TargetAddress != "" {
		t.Fatalf("cleared spec.targetAddress = %q, want empty", stored.Spec.TargetAddress)
	}
}

func TestPortForwardReconcilerResolvesStableNodePortTarget(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{
		Name:              "primary",
		Address:           "192.0.2.1",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP, NodePort: 30080}},
		},
	}
	nodeHigh := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-z"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.20"}}},
	}
	nodeLow := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
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
			TargetPort:   30080,
			ServiceRef:   &api.NamespacedName{Namespace: service.Namespace, Name: service.Name},
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &service, &nodeHigh, &nodeLow, &forward).
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
	if got.Target != "192.0.2.10" {
		t.Fatalf("dst-nat to-addresses = %q, want stable node InternalIP 192.0.2.10", got.Target)
	}
	var stored api.MikroTikPortForward
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: forward.Namespace, Name: forward.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.TargetAddress != "192.0.2.10" {
		t.Fatalf("status.targetAddress = %q, want 192.0.2.10", stored.Status.TargetAddress)
	}
}
