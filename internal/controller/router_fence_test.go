package controller

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRouterFenceSerializesSameKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := newRouterFenceRegistry()
		key := types.NamespacedName{Namespace: "app", Name: "router"}
		var inCritical atomic.Int32
		var overlapped atomic.Bool
		releaseHold := make(chan struct{})
		firstErr := make(chan error, 1)
		secondErr := make(chan error, 1)

		enter := func() {
			if inCritical.Add(1) != 1 {
				overlapped.Store(true)
			}
		}
		leave := func() {
			inCritical.Add(-1)
		}

		go func() {
			firstErr <- registry.withFence(context.Background(), key, func() error {
				enter()
				defer leave()
				<-releaseHold
				return nil
			})
		}()
		synctest.Wait()

		go func() {
			secondErr <- registry.withFence(context.Background(), key, func() error {
				enter()
				leave()
				return nil
			})
		}()
		synctest.Wait()

		if inCritical.Load() != 1 {
			t.Fatalf("in-critical = %d, want 1 holder while waiter is blocked", inCritical.Load())
		}
		if overlapped.Load() {
			t.Fatal("same-key fence allowed overlapping RouterOS operations")
		}

		close(releaseHold)
		synctest.Wait()

		if err := <-firstErr; err != nil {
			t.Fatalf("holder error = %v", err)
		}
		if err := <-secondErr; err != nil {
			t.Fatalf("waiter error = %v", err)
		}
		if overlapped.Load() {
			t.Fatal("same-key fence allowed overlapping RouterOS operations")
		}
		assertFenceEmpty(t, registry)
	})
}

func TestRouterFenceAllowsDistinctKeysConcurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := newRouterFenceRegistry()
		releaseHold := make(chan struct{})
		var entered atomic.Int32
		errs := make(chan error, 2)

		for _, name := range []string{"router-a", "router-b"} {
			key := types.NamespacedName{Namespace: "app", Name: name}
			go func(key types.NamespacedName) {
				errs <- registry.withFence(context.Background(), key, func() error {
					entered.Add(1)
					<-releaseHold
					return nil
				})
			}(key)
		}
		synctest.Wait()

		if got := entered.Load(); got != 2 {
			t.Fatalf("distinct keys entered = %d, want 2 concurrent holders", got)
		}
		close(releaseHold)
		synctest.Wait()
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("distinct-key error = %v", err)
			}
		}
		assertFenceEmpty(t, registry)
	})
}

func TestWithFenceReturnsOperationErrorAndReleases(t *testing.T) {
	registry := newRouterFenceRegistry()
	key := types.NamespacedName{Namespace: "app", Name: "router"}
	want := errors.New("routeros write failed")

	err := registry.withFence(context.Background(), key, func() error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("withFence error = %v, want %v", err, want)
	}
	assertFenceEmpty(t, registry)

	release, err := registry.acquire(context.Background(), key)
	if err != nil {
		t.Fatalf("acquire after failed operation = %v", err)
	}
	release()
	assertFenceEmpty(t, registry)
}

func TestRouterFenceCanceledWaiterDoesNotLeakEntry(t *testing.T) {
	registry := newRouterFenceRegistry()
	key := types.NamespacedName{Namespace: "app", Name: "router"}
	release, err := registry.acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.acquire(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire error = %v, want context.Canceled", err)
	}

	registry.mu.Lock()
	entry := registry.entries[key]
	refs := 0
	if entry != nil {
		refs = entry.refs
	}
	entries := len(registry.entries)
	registry.mu.Unlock()
	if entries != 1 || refs != 1 {
		t.Fatalf("canceled waiter leaked fence state: entries=%d refs=%d", entries, refs)
	}

	release()
	assertFenceEmpty(t, registry)
}

func TestWithRouterConnectionsRereadsRouterAfterWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scheme := controllerTestScheme(t)
		endpoint := api.RouterEndpoint{
			Name:              "primary",
			Address:           "192.0.2.10",
			CredentialsSecret: corev1.LocalObjectReference{Name: "credentials"},
		}
		router := api.MikroTikRouter{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "fence-reread",
				Namespace:  "app",
				Finalizers: []string{resourceFinalizer},
			},
			Spec:   api.MikroTikRouterSpec{Routers: []api.RouterEndpoint{endpoint}},
			Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{endpoint}},
		}
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "app"}}
		kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router, &secret).Build()

		var dials atomic.Int32
		factory := func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials.Add(1)
			return &recordingRouterClient{}, nil
		}
		key := types.NamespacedName{Namespace: router.Namespace, Name: router.Name}
		hold := make(chan struct{})
		firstErr := make(chan error, 1)
		secondErr := make(chan error, 1)
		var waiterOperated atomic.Bool

		go func() {
			firstErr <- withRouterConnections(
				context.Background(),
				kube,
				factory,
				key,
				true,
				func(api.MikroTikRouter, []routerConnection) error {
					<-hold
					return nil
				},
			)
		}()
		synctest.Wait()

		if err := kube.Delete(context.Background(), &router); err != nil {
			t.Fatal(err)
		}

		go func() {
			secondErr <- withRouterConnections(
				context.Background(),
				kube,
				factory,
				key,
				true,
				func(api.MikroTikRouter, []routerConnection) error {
					waiterOperated.Store(true)
					return nil
				},
			)
		}()
		synctest.Wait()

		close(hold)
		synctest.Wait()

		if err := <-firstErr; err != nil {
			t.Fatalf("holder error = %v", err)
		}
		err := <-secondErr
		if err == nil {
			t.Fatal("waiter connected to a router that was deleted while waiting")
		}
		if waiterOperated.Load() {
			t.Fatal("waiter ran RouterOS operations against a deleted router")
		}
		if !apierrors.IsNotFound(err) && !strings.Contains(err.Error(), "being deleted") {
			t.Fatalf("waiter error = %v, want deleted-router rejection", err)
		}
		if got := dials.Load(); got != 1 {
			t.Fatalf("dials = %d, want 1 (waiter must re-read and skip a terminating router)", got)
		}
	})
}

func TestWithRouterConnectionsCleanupSkipsDialWhenNoEndpoints(t *testing.T) {
	scheme := controllerTestScheme(t)
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "fence-empty", Namespace: "app"},
		Status: api.MikroTikRouterStatus{AppliedEndpoints: []api.RouterEndpoint{{
			Name: "stale",
		}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&router).Build()
	dials := 0
	var gotConnections []routerConnection
	err := withRouterConnections(
		context.Background(),
		kube,
		func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return &recordingRouterClient{}, nil
		},
		types.NamespacedName{Namespace: router.Namespace, Name: router.Name},
		false,
		func(_ api.MikroTikRouter, connections []routerConnection) error {
			gotConnections = connections
			return nil
		},
	)
	if err != nil {
		t.Fatalf("cleanup with empty endpoints error = %v", err)
	}
	if dials != 0 {
		t.Fatalf("cleanup dialed RouterOS %d times with no usable endpoints", dials)
	}
	if gotConnections != nil {
		t.Fatalf("connections = %#v, want nil", gotConnections)
	}
}

func assertFenceEmpty(t *testing.T, registry *routerFenceRegistry) {
	t.Helper()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.entries) != 0 {
		t.Fatalf("fence entries leaked: %#v", registry.entries)
	}
}
