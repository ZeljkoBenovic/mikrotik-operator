package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestRouteReconcilerAppliesDirectRouteWithDistance(t *testing.T) {
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
	route := api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikRouteSpec{
			RouterRef:   router.Name,
			Destination: "10.0.0.8/32",
			Gateway:     "192.0.2.1",
			Distance:    20,
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &route).
		WithStatusSubresource(&router, &route).
		Build()
	reconciler := RouteReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(route.Namespace, route.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredRouteDestinations) != 1 || routerClient.ensuredRouteDestinations[0] != "10.0.0.8/32" {
		t.Fatalf("destinations = %#v, want [10.0.0.8/32]", routerClient.ensuredRouteDestinations)
	}
	if len(routerClient.ensuredRouteGateways) != 1 || routerClient.ensuredRouteGateways[0] != "192.0.2.1" {
		t.Fatalf("gateways = %#v, want [192.0.2.1]", routerClient.ensuredRouteGateways)
	}
	if len(routerClient.ensuredRouteDistances) != 1 || routerClient.ensuredRouteDistances[0] != 20 {
		t.Fatalf("distances = %#v, want [20]", routerClient.ensuredRouteDistances)
	}
	var stored api.MikroTikRoute
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: route.Namespace, Name: route.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied || stored.Status.RouterRef != router.Name {
		t.Fatalf("status = %#v, want applied on %s", stored.Status, router.Name)
	}
	if len(stored.Status.Conditions) != 1 || stored.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("ready condition = %#v", stored.Status.Conditions)
	}
}

func TestRouteReconcilerRejectsMissingDestinationOrGateway(t *testing.T) {
	scheme := controllerTestScheme(t)
	route := api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "incomplete", Namespace: "app"},
		Spec:       api.MikroTikRouteSpec{Gateway: "192.0.2.1"},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&route).
		WithStatusSubresource(&route).
		Build()
	dials := 0
	reconciler := RouteReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return nil, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(route.Namespace, route.Name)); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("invalid route dialed RouterOS %d times", dials)
	}
	var stored api.MikroTikRoute
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: route.Namespace, Name: route.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Applied {
		t.Fatal("incomplete route marked applied")
	}
	if len(stored.Status.Conditions) == 0 || !strings.Contains(stored.Status.Conditions[0].Message, "destination and gateway") {
		t.Fatalf("status message = %#v, want destination and gateway error", stored.Status.Conditions)
	}
}

func TestRouteReconcilerIgnoresMissingObject(t *testing.T) {
	scheme := controllerTestScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := RouteReconciler{Client: kube}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "missing")); err != nil {
		t.Fatal(err)
	}
}

func TestRouteReconcilerDeletesRouterOSOnDeletion(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	now := metav1.NewTime(time.Now())
	route := api.MikroTikRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "web",
			Namespace:         "app",
			Finalizers:        []string{resourceFinalizer},
			DeletionTimestamp: &now,
			Annotations:       map[string]string{durableRouterTargetsAnnotation: "router-a,router-b"},
		},
		Spec:   api.MikroTikRouteSpec{Destination: "10.0.0.8/32", Gateway: "192.0.2.1", RouterRef: "router-b"},
		Status: api.MikroTikRouteStatus{RouterRef: "router-b", Applied: true},
	}
	objects = append(objects, &route)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&route).Build()
	reconciler := RouteReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(route.Namespace, route.Name)); err != nil {
		t.Fatal(err)
	}
	wantComment := ros.ManagedComment("route", route.Name, route.Namespace)
	for name, routerClient := range clients {
		if len(routerClient.deletedRouteComments) == 0 {
			t.Fatalf("%s was not cleaned: deletedRouteComments=%#v", name, routerClient.deletedRouteComments)
		}
		if routerClient.deletedRouteComments[0] != wantComment {
			t.Fatalf("%s deleted comments %#v, want %q", name, routerClient.deletedRouteComments, wantComment)
		}
	}
	var stored api.MikroTikRoute
	err := kube.Get(context.Background(), types.NamespacedName{Namespace: route.Namespace, Name: route.Name}, &stored)
	if err == nil && controllerutil.ContainsFinalizer(&stored, resourceFinalizer) {
		t.Fatal("deletion left the managed-config finalizer")
	}
}
