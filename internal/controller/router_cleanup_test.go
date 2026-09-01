package controller

import (
	"context"
	"testing"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestRouterReconcilerSweepsManagedConfigOnDeletion(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{
		Name:              "primary",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	now := metav1.NewTime(time.Now())
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "router",
			Namespace:         "app",
			Finalizers:        []string{resourceFinalizer},
			DeletionTimestamp: &now,
		},
		Spec:   api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret).
		WithStatusSubresource(&router).
		Build()
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			return routerClient, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(router.Namespace, router.Name)); err != nil {
		t.Fatal(err)
	}
	if routerClient.deletedManaged != 1 {
		t.Fatalf("DeleteManagedConfiguration calls = %d, want 1", routerClient.deletedManaged)
	}
	var stored api.MikroTikRouter
	err := kube.Get(context.Background(), types.NamespacedName{Namespace: router.Namespace, Name: router.Name}, &stored)
	if err == nil && controllerutil.ContainsFinalizer(&stored, resourceFinalizer) {
		t.Fatal("router deletion left the managed-config finalizer")
	}
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestRouterReconcilerSkipsDeletionSweepWhenEndpointIsClaimed(t *testing.T) {
	scheme := controllerTestScheme(t)
	endpoint := api.RouterEndpoint{
		Name:              "primary",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	now := metav1.NewTime(time.Now())
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "old",
			Namespace:         "app",
			Finalizers:        []string{resourceFinalizer},
			DeletionTimestamp: &now,
		},
		Spec:   api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
	}
	claimant := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: "app"},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	dials := 0
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &claimant, &secret).
		WithStatusSubresource(&router, &claimant).
		Build()
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return &recordingRouterClient{}, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(router.Namespace, router.Name)); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("claimed endpoint was swept %d times", dials)
	}
	var stored api.MikroTikRouter
	err := kube.Get(context.Background(), types.NamespacedName{Namespace: router.Namespace, Name: router.Name}, &stored)
	if err == nil && controllerutil.ContainsFinalizer(&stored, resourceFinalizer) {
		t.Fatal("router deletion left the managed-config finalizer after skipping the claimed endpoint")
	}
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestRouterReconcilerSweepsRemovedEndpoints(t *testing.T) {
	scheme := controllerTestScheme(t)
	current := api.RouterEndpoint{
		Name:              "keep",
		Address:           "192.0.2.10",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	removed := api.RouterEndpoint{
		Name:              "gone",
		Address:           "192.0.2.11",
		CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "app", Finalizers: []string{resourceFinalizer}},
		Spec:       api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{current}},
		Status:     api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{current, removed}},
	}
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
	clients := map[string]*recordingRouterClient{
		current.Address: {},
		removed.Address: {},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret).
		WithStatusSubresource(&router).
		Build()
	reconciler := RouterReconciler{
		Client: kube,
		Scheme: scheme,
		Factory: func(_ context.Context, address string, _ int32, _ bool, _, _ string) (ros.Client, error) {
			client, ok := clients[address]
			if !ok {
				t.Fatalf("unexpected router address %s", address)
			}
			return client, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(router.Namespace, router.Name)); err != nil {
		t.Fatal(err)
	}
	if clients[removed.Address].deletedManaged != 1 {
		t.Fatalf("removed endpoint cleanup calls = %d, want 1", clients[removed.Address].deletedManaged)
	}
	if clients[current.Address].deletedManaged != 0 {
		t.Fatal("current endpoint was swept while remaining in spec")
	}
	var stored api.MikroTikRouter
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: router.Namespace, Name: router.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Status.AppliedEndpoints) != 1 || stored.Status.AppliedEndpoints[0].Address != current.Address {
		t.Fatalf("applied endpoints = %#v, want only %s", stored.Status.AppliedEndpoints, current.Address)
	}
}
