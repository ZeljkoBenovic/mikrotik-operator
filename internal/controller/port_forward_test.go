package controller

import (
	"context"
	"errors"
	"strconv"
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

func TestPreparePortForwardReconcileRequest(t *testing.T) {
	scheme := controllerTestScheme(t)
	web := types.NamespacedName{Namespace: "app", Name: "web"}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}},
	}
	clusterIP := func(name string, ports ...corev1.ServicePort) corev1.Service {
		return corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.8", Ports: ports},
		}
	}
	tests := []struct {
		name        string
		objects     []client.Object
		request     portForwardReconcileRequest
		wantErr     error
		wantErrText string
		want        []portForwardCandidate
	}{
		{
			name: "empty public-ip skips NAT candidates",
			request: portForwardReconcileRequest{
				publicIP:  "",
				routerRef: "router",
				services:  []corev1.Service{clusterIP("web", corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolTCP})},
			},
		},
		{
			name: "empty protocol defaults to tcp",
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "service/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services:   []corev1.Service{clusterIP("web", corev1.ServicePort{Port: 80})},
			},
			want: []portForwardCandidate{{
				service:      web,
				protocol:     "tcp",
				externalPort: 80,
				targetPort:   80,
			}},
		},
		{
			name: "tcp and udp on the same port are distinct candidates",
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "service/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services: []corev1.Service{clusterIP("web",
					corev1.ServicePort{Port: 53, Protocol: corev1.ProtocolTCP},
					corev1.ServicePort{Port: 53, Protocol: corev1.ProtocolUDP},
				)},
			},
			want: []portForwardCandidate{
				{service: web, protocol: "tcp", externalPort: 53, targetPort: 53},
				{service: web, protocol: "udp", externalPort: 53, targetPort: 53},
			},
		},
		{
			name: "requireSelectedPorts ignores unused Service ports",
			request: portForwardReconcileRequest{
				namespace:            "app",
				sourceName:           "ingress/web",
				publicIP:             "198.51.100.10",
				routerRef:            "router",
				requireSelectedPorts: true,
				servicePorts: map[types.NamespacedName][]corev1.ServicePort{
					web: {{Port: 80, Protocol: corev1.ProtocolTCP}},
				},
				services: []corev1.Service{clusterIP("web",
					corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolTCP},
					corev1.ServicePort{Port: 443, Protocol: corev1.ProtocolTCP},
				)},
			},
			want: []portForwardCandidate{{
				service:      web,
				protocol:     "tcp",
				externalPort: 80,
				targetPort:   80,
			}},
		},
		{
			name: "headless ClusterIP is skipped",
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "service/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services: []corev1.Service{{
					ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
					Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone, Ports: []corev1.ServicePort{{Port: 80}}},
				}},
			},
		},
		{
			name: "same public match to different Services is ambiguous",
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "ingress/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services: []corev1.Service{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
						Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.8", Ports: []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "app"},
						Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.9", Ports: []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}},
					},
				},
			},
			wantErr: errGeneratedChildAmbiguity,
		},
		{
			name: "duplicate paths to the same Service keep one candidate",
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "ingress/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services: []corev1.Service{
					clusterIP("web", corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolTCP}),
					clusterIP("web", corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolTCP}),
				},
			},
			want: []portForwardCandidate{{
				service:      web,
				protocol:     "tcp",
				externalPort: 80,
				targetPort:   80,
			}},
		},
		{
			name:    "NodePort without an allocated port is rejected",
			objects: []client.Object{&node},
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "service/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services: []corev1.Service{{
					ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
					Spec: corev1.ServiceSpec{
						Type:      corev1.ServiceTypeNodePort,
						ClusterIP: "10.0.0.8",
						Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
					},
				}},
			},
			wantErrText: "has no NodePort for port 80",
		},
		{
			name:    "NodePort uses node InternalIP and NodePort",
			objects: []client.Object{&node},
			request: portForwardReconcileRequest{
				namespace:  "app",
				sourceName: "service/web",
				publicIP:   "198.51.100.10",
				routerRef:  "router",
				services: []corev1.Service{{
					ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
					Spec: corev1.ServiceSpec{
						Type:      corev1.ServiceTypeNodePort,
						ClusterIP: "10.0.0.8",
						Ports:     []corev1.ServicePort{{Port: 80, NodePort: 30080, Protocol: corev1.ProtocolTCP}},
					},
				}},
			},
			want: []portForwardCandidate{{
				service:       web,
				protocol:      "tcp",
				externalPort:  80,
				targetAddress: "192.0.2.10",
				targetPort:    30080,
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(test.objects...).Build()
			test.request.kube = kube
			got, err := preparePortForwardReconcileRequest(context.Background(), test.request)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("error = %v, want text %q", err, test.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.candidates) != len(test.want) {
				t.Fatalf("candidates = %#v, want %#v", got.candidates, test.want)
			}
			for i, want := range test.want {
				gotCandidate := got.candidates[i]
				want.name = "pf-" + shortHash(strings.Join([]string{
					test.request.namespace,
					test.request.sourceName,
					want.service.Namespace,
					want.service.Name,
					strconv.Itoa(int(want.externalPort)),
					want.protocol,
				}, "/"))
				if gotCandidate != want {
					t.Fatalf("candidate[%d] = %#v, want %#v", i, gotCandidate, want)
				}
			}
		})
	}
}

func TestReconcileServicePortForwardsHonorsSelectedPortsAndPrunesStale(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "svc-uid"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.43.0.10",
			Ports: []corev1.ServicePort{
				{Port: 80, Protocol: corev1.ProtocolTCP},
				{Port: 443, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	staleName := "pf-" + shortHash("app/ingress/web/app/web/443/tcp")
	stale := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{
			Name:      staleName,
			Namespace: "app",
			Labels:    map[string]string{"mikrotik.operator.io/port-forward-source": shortHash("app/ingress/web")},
		},
		Spec: api.MikroTikPortForwardSpec{Protocol: "tcp", ExternalPort: 443, TargetPort: 443},
	}
	if err := controllerutil.SetControllerReference(&service, &stale, scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &stale).Build()
	if err := reconcileServicePortForwards(context.Background(), portForwardReconcileRequest{
		kube:                 kube,
		scheme:               scheme,
		owner:                &service,
		sourceName:           "ingress/web",
		namespace:            service.Namespace,
		publicIP:             "198.51.100.10",
		routerRef:            "home-router",
		services:             []corev1.Service{service},
		requireSelectedPorts: true,
		servicePorts: map[types.NamespacedName][]corev1.ServicePort{
			{Namespace: "app", Name: "web"}: {{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var list api.MikroTikPortForwardList
	if err := kube.List(context.Background(), &list, client.InNamespace(service.Namespace)); err != nil {
		t.Fatal(err)
	}
	owned := make([]api.MikroTikPortForward, 0)
	for _, forward := range list.Items {
		if metav1.IsControlledBy(&forward, &service) {
			owned = append(owned, forward)
		}
	}
	if len(owned) != 1 {
		t.Fatalf("owned port forwards = %d, want 1 selected backend port", len(owned))
	}
	if owned[0].Spec.ExternalPort != 80 || owned[0].Spec.Protocol != "tcp" {
		t.Fatalf("unexpected selected port forward: %#v", owned[0].Spec)
	}
	assertNotFound(t, kube, &api.MikroTikPortForward{}, "app", staleName)
}

func TestReconcileServicePortForwardsRejectsUnownedNameCollision(t *testing.T) {
	scheme := controllerTestScheme(t)
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: "svc-uid"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	name := "pf-" + shortHash("app/service/web/app/web/80/tcp")
	unowned := api.MikroTikPortForward{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app"},
		Spec:       api.MikroTikPortForwardSpec{Protocol: "udp", ExternalPort: 9, TargetAddress: "10.0.0.99"},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&service, &unowned).Build()
	err := reconcileServicePortForwards(context.Background(), portForwardReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      &service,
		sourceName: "service/web",
		namespace:  service.Namespace,
		publicIP:   "198.51.100.10",
		routerRef:  "home-router",
		services:   []corev1.Service{service},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists and is not owned") {
		t.Fatalf("error = %v, want ownership collision", err)
	}
	var stored api.MikroTikPortForward
	if getErr := kube.Get(context.Background(), types.NamespacedName{Name: unowned.Name, Namespace: unowned.Namespace}, &stored); getErr != nil {
		t.Fatal(getErr)
	}
	if stored.Spec.Protocol != "udp" || stored.Spec.TargetAddress != "10.0.0.99" || metav1.IsControlledBy(&stored, &service) {
		t.Fatalf("unowned port forward was mutated: %#v", stored)
	}
}
