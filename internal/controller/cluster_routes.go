package controller

import (
	"context"
	"fmt"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const clusterRouteSourceLabel = "mikrotik.operator.io/route-source"
const clusterRouteOriginLabel = "mikrotik.operator.io/cluster-route-origin"
const clusterRouteOriginOverride = "override"
const clusterRouteOriginNodes = "nodes"
const clusterRouteOriginBoth = "both"

type clusterRouteReconcileRequest struct {
	kube       client.Client
	scheme     *runtime.Scheme
	owner      client.Object
	sourceName string
	namespace  string
	routerRef  string
	services   []corev1.Service
}

type clusterRouteCandidate struct {
	name        string
	destination string
	gateway     string
	origin      string
}

type clusterRouteHop struct {
	gateway string
	origin  string
}

func reconcileOwnedClusterRoutes(ctx context.Context, request clusterRouteReconcileRequest) error {
	candidates, err := desiredClusterRouteCandidates(ctx, request)
	if err != nil {
		return err
	}
	labelValue := clusterRouteSourceValue(request.namespace, request.sourceName)
	var existing api.MikroTikRouteList
	if err := request.kube.List(ctx, &existing, client.InNamespace(request.namespace)); err != nil {
		return err
	}
	desired := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		desired[candidate.name] = true
		var route api.MikroTikRoute
		err := request.kube.Get(ctx, types.NamespacedName{Name: candidate.name, Namespace: request.namespace}, &route)
		if apierrors.IsNotFound(err) {
			route = api.MikroTikRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      candidate.name,
					Namespace: request.namespace,
					Labels: map[string]string{
						clusterRouteSourceLabel: labelValue,
						clusterRouteOriginLabel: candidate.origin,
					},
				},
			}
			if err := controllerutil.SetControllerReference(request.owner, &route, request.scheme); err != nil {
				return err
			}
			route.Spec = api.MikroTikRouteSpec{
				RouterRef:   request.routerRef,
				Destination: candidate.destination,
				Gateway:     candidate.gateway,
			}
			if err := request.kube.Create(ctx, &route); err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !metav1.IsControlledBy(&route, request.owner) {
			return fmt.Errorf(
				"route %s/%s already exists and is not owned by %s %s/%s",
				route.Namespace,
				route.Name,
				request.owner.GetObjectKind().GroupVersionKind().Kind,
				request.owner.GetNamespace(),
				request.owner.GetName(),
			)
		}
		if route.Spec.RouterRef == request.routerRef &&
			route.Spec.Destination == candidate.destination &&
			route.Spec.Gateway == candidate.gateway &&
			route.Labels[clusterRouteOriginLabel] == candidate.origin {
			continue
		}
		if route.Labels == nil {
			route.Labels = map[string]string{}
		}
		route.Labels[clusterRouteSourceLabel] = labelValue
		route.Labels[clusterRouteOriginLabel] = candidate.origin
		route.Spec.RouterRef = request.routerRef
		route.Spec.Destination = candidate.destination
		route.Spec.Gateway = candidate.gateway
		if err := request.kube.Update(ctx, &route); err != nil {
			return err
		}
	}
	for index := range existing.Items {
		route := &existing.Items[index]
		if desired[route.Name] || !metav1.IsControlledBy(route, request.owner) {
			continue
		}
		if err := request.kube.Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func desiredClusterRouteCandidates(ctx context.Context, request clusterRouteReconcileRequest) ([]clusterRouteCandidate, error) {
	candidates := make([]clusterRouteCandidate, 0)
	seen := make(map[string]struct{})
	for _, service := range request.services {
		if !isClusterIPService(service) {
			continue
		}
		if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
			continue
		}
		gateways, err := clusterRouteHops(ctx, request.kube, service, request.namespace, request.routerRef)
		if err != nil {
			return nil, err
		}
		destination := service.Spec.ClusterIP + "/32"
		for _, hop := range gateways {
			name := "rt-" + shortHash(
				request.namespace+"/"+request.sourceName+"/"+service.Namespace+"/"+service.Name+"/"+destination+"/"+hop.gateway,
			)
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			candidates = append(candidates, clusterRouteCandidate{
				name:        name,
				destination: destination,
				gateway:     hop.gateway,
				origin:      hop.origin,
			})
		}
	}
	return candidates, nil
}

func clusterRouteGateways(
	ctx context.Context,
	kube client.Client,
	service corev1.Service,
	ownerNamespace string,
	routerRef string,
) ([]string, error) {
	hops, err := clusterRouteHops(ctx, kube, service, ownerNamespace, routerRef)
	if err != nil {
		return nil, err
	}
	gateways := make([]string, 0, len(hops))
	for _, hop := range hops {
		gateways = append(gateways, hop.gateway)
	}
	return gateways, nil
}

func clusterRouteHops(
	ctx context.Context,
	kube client.Client,
	service corev1.Service,
	ownerNamespace string,
	routerRef string,
) ([]clusterRouteHop, error) {
	if routerRef == "" {
		nodes, err := routeGateways(ctx, kube, service)
		if err != nil {
			return nil, err
		}
		return hopsWithOrigin(nodes, clusterRouteOriginNodes), nil
	}
	key := routerKeyFromRef(ownerNamespace, routerRef)
	var router api.MikroTikRouter
	if err := kube.Get(ctx, key, &router); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("MikroTikRouter %s/%s not found: %w", key.Namespace, key.Name, err)
		}
		return nil, err
	}
	return desiredClusterRouteHops(ctx, kube, service, router)
}

func desiredClusterRouteHops(
	ctx context.Context,
	kube client.Client,
	service corev1.Service,
	router api.MikroTikRouter,
) ([]clusterRouteHop, error) {
	hops := make([]clusterRouteHop, 0)
	seen := make(map[string]string)
	add := func(values []string, origin string) {
		for _, value := range values {
			if value == "" {
				continue
			}
			if existing, exists := seen[value]; exists {
				seen[value] = mergeClusterRouteOrigin(existing, origin)
				continue
			}
			seen[value] = origin
			hops = append(hops, clusterRouteHop{gateway: value, origin: origin})
		}
	}
	var nodeIPs []string
	var nodeErr error
	nodesLoaded := false
	loadNodes := func() ([]string, error) {
		if !nodesLoaded {
			nodesLoaded = true
			nodeIPs, nodeErr = routeGateways(ctx, kube, service)
		}
		return nodeIPs, nodeErr
	}
	for _, endpoint := range routerEndpoints(router) {
		if want := endpointRouteGateway(endpoint, router); want != "" {
			add([]string{want}, clusterRouteOriginOverride)
			continue
		}
		nodes, err := loadNodes()
		if err != nil {
			return nil, err
		}
		add(nodes, clusterRouteOriginNodes)
	}
	if len(hops) == 0 {
		nodes, err := loadNodes()
		if err != nil {
			return nil, err
		}
		add(nodes, clusterRouteOriginNodes)
	}
	for index := range hops {
		hops[index].origin = seen[hops[index].gateway]
	}
	return hops, nil
}

func hopsWithOrigin(gateways []string, origin string) []clusterRouteHop {
	hops := make([]clusterRouteHop, 0, len(gateways))
	for _, gateway := range gateways {
		hops = append(hops, clusterRouteHop{gateway: gateway, origin: origin})
	}
	return hops
}

func mergeClusterRouteOrigin(existing, added string) string {
	if existing == "" || existing == added {
		return added
	}
	return clusterRouteOriginBoth
}

func endpointRouteGateway(endpoint api.RouterEndpoint, router api.MikroTikRouter) string {
	if endpoint.RouteGateway != "" {
		return endpoint.RouteGateway
	}
	return router.Spec.RouteGateway
}

func clusterRouteAppliesToEndpoint(gateway, origin string, endpoint api.RouterEndpoint, router api.MikroTikRouter) bool {
	want := endpointRouteGateway(endpoint, router)
	if want != "" {
		return gateway == want
	}
	return origin == clusterRouteOriginNodes || origin == clusterRouteOriginBoth
}

func clusterRouteSourceValue(namespace, sourceName string) string {
	return shortHash(namespace + "/" + sourceName)
}

func deleteLabeledClusterRoutes(ctx context.Context, kube client.Client, namespace, sourceName string) error {
	var existing api.MikroTikRouteList
	if err := kube.List(
		ctx,
		&existing,
		client.InNamespace(namespace),
		client.MatchingLabels{clusterRouteSourceLabel: clusterRouteSourceValue(namespace, sourceName)},
	); err != nil {
		return err
	}
	for index := range existing.Items {
		if err := kube.Delete(ctx, &existing.Items[index]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func isClusterIPService(service corev1.Service) bool {
	return service.Spec.Type == "" || service.Spec.Type == corev1.ServiceTypeClusterIP
}

func translatorOwnsGeneratedChildren(obj client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		switch ref.Kind {
		case "Service", "Ingress", "HTTPRoute":
			return true
		}
	}
	return false
}
