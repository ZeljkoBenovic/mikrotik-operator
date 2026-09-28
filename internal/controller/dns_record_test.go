package controller

import (
	"context"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestDNSReconcilerAddsFinalizerThenAppliesHostnameTTLAndComment(t *testing.T) {
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
	record := api.MikroTikDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: api.MikroTikDNSRecordSpec{
			RouterRef: router.Name,
			Name:      "web.home.arpa",
			Address:   "10.0.0.8",
			TTL:       "1h",
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &record).
		WithStatusSubresource(&router, &record).
		Build()
	reconciler := DNSReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}

	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.ensuredDNS != 0 {
		t.Fatalf("first reconcile dialed RouterOS %d times", routerClient.ensuredDNS)
	}
	var stored api.MikroTikDNSRecord
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: record.Namespace, Name: record.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&stored, resourceFinalizer) {
		t.Fatal("first reconcile did not add the managed-config finalizer")
	}

	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(record.Namespace, record.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredDNSNames) != 1 || routerClient.ensuredDNSNames[0] != "web.home.arpa" {
		t.Fatalf("DNS names = %#v, want [web.home.arpa]", routerClient.ensuredDNSNames)
	}
	if len(routerClient.ensuredDNSAddresses) != 1 || routerClient.ensuredDNSAddresses[0] != "10.0.0.8" {
		t.Fatalf("DNS addresses = %#v, want [10.0.0.8]", routerClient.ensuredDNSAddresses)
	}
	if len(routerClient.ensuredDNSTTLs) != 1 || routerClient.ensuredDNSTTLs[0] != "1h" {
		t.Fatalf("DNS TTLs = %#v, want [1h]", routerClient.ensuredDNSTTLs)
	}
	wantComment := ros.ManagedComment("dns", record.Name, record.Namespace)
	if len(routerClient.ensuredDNSComments) != 1 || routerClient.ensuredDNSComments[0] != wantComment {
		t.Fatalf("DNS comments = %#v, want %q", routerClient.ensuredDNSComments, wantComment)
	}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: record.Namespace, Name: record.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied || stored.Status.RouterRef != router.Name {
		t.Fatalf("status = %#v, want applied on %s", stored.Status, router.Name)
	}
}

func TestDNSReconcilerIgnoresMissingObject(t *testing.T) {
	scheme := controllerTestScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := DNSReconciler{Client: kube}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "missing")); err != nil {
		t.Fatal(err)
	}
}
