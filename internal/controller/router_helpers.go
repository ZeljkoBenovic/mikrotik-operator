package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type routerConnection struct {
	Endpoint api.RouterEndpoint
	Client   ros.Client
}

func ensureRouterActive(ctx context.Context, kube client.Client, router api.MikroTikRouter) error {
	if !router.DeletionTimestamp.IsZero() {
		return fmt.Errorf("MikroTikRouter %s/%s is being deleted", router.Namespace, router.Name)
	}
	if !controllerutil.ContainsFinalizer(&router, resourceFinalizer) {
		return fmt.Errorf("MikroTikRouter %s/%s is not finalized for external cleanup", router.Namespace, router.Name)
	}
	if !routerHasDurableCurrentEndpoints(router) {
		return fmt.Errorf("MikroTikRouter %s/%s current endpoints are not durably recorded", router.Namespace, router.Name)
	}
	return ensureRouterEndpointOwnership(ctx, kube, router)
}

func routerHasDurableCurrentEndpoints(router api.MikroTikRouter) bool {
	durable := make(map[string]struct{}, len(router.Status.AppliedEndpoints))
	for _, endpoint := range router.Status.AppliedEndpoints {
		durable[endpointKey(endpoint)] = struct{}{}
	}
	current := make(map[string]struct{}, len(routerEndpoints(router)))
	for _, endpoint := range routerEndpoints(router) {
		key := endpointKey(endpoint)
		current[key] = struct{}{}
		if _, exists := durable[key]; !exists {
			return false
		}
	}
	for key := range durable {
		if _, exists := current[key]; !exists {
			return false
		}
	}
	return len(current) > 0
}

func ensureRouterEndpointOwnership(ctx context.Context, kube client.Client, router api.MikroTikRouter) error {
	var routers api.MikroTikRouterList
	if err := kube.List(ctx, &routers); err != nil {
		return err
	}
	currentOwner := router.Namespace + "/" + router.Name
	seen := make(map[string]struct{})
	for _, endpoint := range routerEndpoints(router) {
		key := endpointKey(endpoint)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("MikroTikRouter %s has duplicate endpoint %s", currentOwner, endpoint.Address)
		}
		seen[key] = struct{}{}
		owners := []string{currentOwner}
		for _, candidate := range routers.Items {
			if !candidate.DeletionTimestamp.IsZero() || (candidate.Namespace == router.Namespace && candidate.Name == router.Name) {
				continue
			}
			for _, candidateEndpoint := range routerEndpoints(candidate) {
				if endpointKey(candidateEndpoint) == key {
					owners = append(owners, candidate.Namespace+"/"+candidate.Name)
					break
				}
			}
		}
		sort.Strings(owners)
		if owners[0] != currentOwner {
			return fmt.Errorf("MikroTikRouter %s endpoint %s is owned by %s", currentOwner, endpoint.Address, owners[0])
		}
	}
	return nil
}

func endpointClaimedByOtherRouter(ctx context.Context, kube client.Client, router api.MikroTikRouter, endpoint api.RouterEndpoint) (bool, error) {
	var routers api.MikroTikRouterList
	if err := kube.List(ctx, &routers); err != nil {
		return false, err
	}
	key := endpointKey(endpoint)
	for _, candidate := range routers.Items {
		if !candidate.DeletionTimestamp.IsZero() || (candidate.Namespace == router.Namespace && candidate.Name == router.Name) {
			continue
		}
		for _, candidateEndpoint := range routerEndpoints(candidate) {
			if endpointKey(candidateEndpoint) == key {
				return true, nil
			}
		}
	}
	return false, nil
}

func durableRouterTargets(object client.Object, additional ...string) []string {
	seen := make(map[string]struct{})
	targets := make([]string, 0, len(additional)+1)
	add := func(target string) {
		target = strings.TrimSpace(target)
		if target == "" {
			return
		}
		if _, exists := seen[target]; exists {
			return
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	for _, target := range strings.Split(object.GetAnnotations()[durableRouterTargetsAnnotation], ",") {
		add(target)
	}
	for _, target := range additional {
		add(target)
	}
	sort.Strings(targets)
	return targets
}

func persistDurableRouterTarget(ctx context.Context, kube client.Client, object client.Object, targets ...string) (bool, error) {
	targets = durableRouterTargets(object, targets...)
	encoded := strings.Join(targets, ",")
	if object.GetAnnotations()[durableRouterTargetsAnnotation] == encoded {
		return false, nil
	}
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[durableRouterTargetsAnnotation] = encoded
	object.SetAnnotations(annotations)
	return true, kube.Update(ctx, object)
}

func compactDurableRouterTarget(ctx context.Context, kube client.Client, object client.Object, target string) (bool, error) {
	annotations := make(map[string]string, len(object.GetAnnotations()))
	for key, value := range object.GetAnnotations() {
		annotations[key] = value
	}
	if target == "" {
		if _, exists := annotations[durableRouterTargetsAnnotation]; !exists {
			return false, nil
		}
		delete(annotations, durableRouterTargetsAnnotation)
	} else {
		if annotations[durableRouterTargetsAnnotation] == target {
			return false, nil
		}
		annotations[durableRouterTargetsAnnotation] = target
	}
	object.SetAnnotations(annotations)
	return true, kube.Update(ctx, object)
}

func cleanupRouterTargets(
	ctx context.Context,
	kube client.Client,
	factory ros.Factory,
	namespace string,
	targets []string,
	exclude string,
	cleanup func(context.Context, ros.Client) error,
) error {
	for _, target := range targets {
		if target == "" || target == exclude {
			continue
		}
		key := routerKeyFromRef(namespace, target)
		if key.Name == "" {
			continue
		}
		if exclude != "" && key == routerKeyFromRef(namespace, exclude) {
			continue
		}
		err := withRouterConnections(ctx, kube, factory, key, false, func(_ api.MikroTikRouter, connections []routerConnection) error {
			for _, connection := range connections {
				if err := cleanup(ctx, connection.Client); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func routerEndpoints(router api.MikroTikRouter) []api.RouterEndpoint {
	if len(router.Spec.Routers) > 0 {
		return router.Spec.Routers
	}
	if strings.TrimSpace(router.Spec.Address) == "" || strings.TrimSpace(router.Spec.CredentialsSecret.Name) == "" {
		return nil
	}
	return []api.RouterEndpoint{{
		Name:              router.Name,
		Address:           router.Spec.Address,
		Port:              router.Spec.Port,
		TLS:               router.Spec.TLS,
		CredentialsSecret: router.Spec.CredentialsSecret,
		RouteGateway:      router.Spec.RouteGateway,
	}}
}

func validateRouterEndpoints(router api.MikroTikRouter) error {
	endpoints := routerEndpoints(router)
	if len(endpoints) == 0 {
		return fmt.Errorf("MikroTikRouter %s/%s requires a legacy address and credentialsSecret or at least one routers entry", router.Namespace, router.Name)
	}
	for index, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Address) == "" {
			return fmt.Errorf("MikroTikRouter %s/%s endpoint %d has an empty address", router.Namespace, router.Name, index)
		}
		if strings.TrimSpace(endpoint.CredentialsSecret.Name) == "" {
			return fmt.Errorf("MikroTikRouter %s/%s endpoint %d has an empty credentialsSecret", router.Namespace, router.Name, index)
		}
	}
	return nil
}

func routerCleanupEndpoints(router api.MikroTikRouter) []api.RouterEndpoint {
	candidates := router.Status.AppliedEndpoints
	if len(candidates) == 0 {
		candidates = routerEndpoints(router)
	}
	endpoints := make([]api.RouterEndpoint, 0, len(candidates))
	for _, endpoint := range candidates {
		if strings.TrimSpace(endpoint.Address) == "" || strings.TrimSpace(endpoint.CredentialsSecret.Name) == "" {
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func connectRouterClients(
	ctx context.Context,
	kube client.Client,
	factory ros.Factory,
	router api.MikroTikRouter,
) ([]routerConnection, error) {
	connections := make([]routerConnection, 0, len(routerEndpoints(router)))
	for _, endpoint := range routerEndpoints(router) {
		if strings.TrimSpace(endpoint.Address) == "" || strings.TrimSpace(endpoint.CredentialsSecret.Name) == "" {
			return nil, fmt.Errorf("MikroTikRouter %s/%s has an invalid endpoint", router.Namespace, router.Name)
		}
		var secret corev1.Secret
		if err := kube.Get(ctx, types.NamespacedName{Name: endpoint.CredentialsSecret.Name, Namespace: router.Namespace}, &secret); err != nil {
			closeRouterConnections(ctx, connections)
			return nil, err
		}
		c, err := factory(
			ctx,
			endpoint.Address,
			endpoint.Port,
			endpoint.TLS,
			string(secret.Data["username"]),
			string(secret.Data["password"]),
		)
		if err != nil {
			closeRouterConnections(ctx, connections)
			return nil, err
		}
		connections = append(connections, routerConnection{Endpoint: endpoint, Client: c})
	}
	return connections, nil
}

func closeRouterConnections(ctx context.Context, connections []routerConnection) {
	logger := ctrl.LoggerFrom(ctx)
	for _, connection := range connections {
		if err := connection.Client.Close(); err != nil {
			logger.Error(
				err,
				"failed to close RouterOS client",
				"endpoint", connection.Endpoint.Address,
				"endpointName", connection.Endpoint.Name,
			)
		}
	}
}
