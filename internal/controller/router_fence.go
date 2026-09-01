package controller

import (
	"context"
	"sync"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// routerOperationFences serialize RouterOS operations with Router endpoint
// sweeps and finalization. The fence is deliberately process-local: the
// shipped deployment and Helm chart enable leader election, so only the
// elected controller process performs reconciliations. Any custom deployment
// with multiple manager replicas must also enable leader election; this is not
// a distributed lock. Each acquisition re-reads Kubernetes state while holding
// the fence instead of relying on the object fetched before waiting.
var routerOperationFences = newRouterFenceRegistry()
var generatedClaimFences = newRouterFenceRegistry()

type routerFenceRegistry struct {
	mu      sync.Mutex
	entries map[types.NamespacedName]*routerFenceEntry
}

type routerFenceEntry struct {
	token chan struct{}
	refs  int
}

func newRouterFenceRegistry() *routerFenceRegistry {
	return &routerFenceRegistry{entries: make(map[types.NamespacedName]*routerFenceEntry)}
}

func (registry *routerFenceRegistry) withFence(ctx context.Context, key types.NamespacedName, operation func() error) error {
	release, err := registry.acquire(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	return operation()
}

func (registry *routerFenceRegistry) acquire(ctx context.Context, key types.NamespacedName) (func(), error) {
	registry.mu.Lock()
	entry := registry.entries[key]
	if entry == nil {
		entry = &routerFenceEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		registry.entries[key] = entry
	}
	entry.refs++
	registry.mu.Unlock()

	select {
	case <-ctx.Done():
		registry.releaseReference(key, entry)
		return nil, ctx.Err()
	case <-entry.token:
	}
	release := func() {
		entry.token <- struct{}{}
		registry.releaseReference(key, entry)
	}
	return release, nil
}

func (registry *routerFenceRegistry) releaseReference(key types.NamespacedName, entry *routerFenceEntry) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry.refs--
	if entry.refs == 0 && registry.entries[key] == entry {
		delete(registry.entries, key)
	}
}

func withRouterConnections(
	ctx context.Context,
	kube client.Client,
	factory ros.Factory,
	key types.NamespacedName,
	requireActive bool,
	operation func(api.MikroTikRouter, []routerConnection) error,
) error {
	key = normalizeRouterLookupKey(key)
	return routerOperationFences.withFence(ctx, key, func() error {
		router, err := getMikroTikRouter(ctx, kube, key)
		if err != nil {
			return err
		}
		if requireActive {
			if err := ensureRouterActive(ctx, kube, router); err != nil {
				return err
			}
		}
		connectionRouter := router
		if !requireActive {
			connectionRouter.Spec.Routers = routerCleanupEndpoints(router)
			connectionRouter.Spec.Address = ""
			if len(connectionRouter.Spec.Routers) == 0 {
				return operation(router, nil)
			}
		}
		connections, err := connectRouterClients(ctx, kube, factory, connectionRouter)
		if err != nil {
			return err
		}
		defer closeRouterConnections(ctx, connections)
		return operation(router, connections)
	})
}
