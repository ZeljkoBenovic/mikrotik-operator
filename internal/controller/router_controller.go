package controller

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RouterReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Factory ros.Factory
}

func (r *RouterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var obj api.MikroTikRouter
	if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &obj)
	}
	if err := validateRouterEndpoints(obj); err != nil {
		return r.failRouter(ctx, &obj, err)
	}
	if !controllerutil.ContainsFinalizer(&obj, resourceFinalizer) {
		controllerutil.AddFinalizer(&obj, resourceFinalizer)
		if err := r.Update(ctx, &obj); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if err := ensureRouterEndpointOwnership(ctx, r.Client, obj); err != nil {
		return r.failRouter(ctx, &obj, err)
	}
	endpointHistory := durableRouterEndpointUnion(obj.Status.AppliedEndpoints, routerEndpoints(obj))
	if !reflect.DeepEqual(endpointHistory, obj.Status.AppliedEndpoints) {
		obj.Status.AppliedEndpoints = endpointHistory
		if err := r.Status().Update(ctx, &obj); err != nil {
			return reconcile.Result{}, err
		}
		// The endpoint history is the recovery record used by deletion and
		// removed-endpoint cleanup. Never contact RouterOS in the same pass that
		// first makes a current endpoint durable.
		return reconcile.Result{}, nil
	}
	lockedObject := obj
	result := reconcile.Result{RequeueAfter: driftCheckInterval}
	finalizing := false
	err := routerOperationFences.withFence(ctx, req.NamespacedName, func() error {
		if err := r.Get(ctx, req.NamespacedName, &lockedObject); err != nil {
			return err
		}
		if !lockedObject.DeletionTimestamp.IsZero() {
			finalizing = true
			var err error
			result, err = r.reconcileDeletionLocked(ctx, &lockedObject)
			return err
		}
		if err := ensureRouterEndpointOwnership(ctx, r.Client, lockedObject); err != nil {
			return err
		}
		if err := r.cleanupRemovedEndpoints(ctx, lockedObject); err != nil {
			return err
		}
		currentEndpoints := routerEndpoints(lockedObject)
		if !reflect.DeepEqual(lockedObject.Status.AppliedEndpoints, currentEndpoints) {
			lockedObject.Status.AppliedEndpoints = currentEndpoints
			result = reconcile.Result{}
			return r.Status().Update(ctx, &lockedObject)
		}
		connections, err := connectRouterClients(ctx, r.Client, r.Factory, lockedObject)
		if err != nil {
			return err
		}
		defer closeRouterConnections(ctx, connections)
		oldStatus := lockedObject.Status
		lockedObject.Status.Connected = true
		lockedObject.Status.AppliedEndpoints = currentEndpoints
		lockedObject.Status.Conditions = readyCondition(
			lockedObject.Status.Conditions,
			metav1.ConditionTrue,
			"Connected",
			"Connected to RouterOS",
		)
		if reflect.DeepEqual(oldStatus, lockedObject.Status) {
			return nil
		}
		return r.Status().Update(ctx, &lockedObject)
	})
	if err != nil {
		if finalizing {
			return result, err
		}
		return r.failRouter(ctx, &lockedObject, err)
	}
	return result, nil
}

func (r *RouterReconciler) reconcileDeletion(ctx context.Context, obj *api.MikroTikRouter) (reconcile.Result, error) {
	key := types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}
	result := reconcile.Result{}
	err := routerOperationFences.withFence(ctx, key, func() error {
		var fresh api.MikroTikRouter
		if err := r.Get(ctx, key, &fresh); err != nil {
			return client.IgnoreNotFound(err)
		}
		if fresh.DeletionTimestamp.IsZero() {
			return nil
		}
		var err error
		result, err = r.reconcileDeletionLocked(ctx, &fresh)
		return err
	})
	return result, err
}

func (r *RouterReconciler) reconcileDeletionLocked(ctx context.Context, obj *api.MikroTikRouter) (reconcile.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, resourceFinalizer) {
		return reconcile.Result{}, nil
	}
	endpoints := routerCleanupEndpoints(*obj)
	if len(endpoints) > 0 {
		oldRouter := *obj
		ownedEndpoints := make([]api.RouterEndpoint, 0, len(endpoints))
		for _, endpoint := range endpoints {
			claimed, err := endpointClaimedByOtherRouter(ctx, r.Client, *obj, endpoint)
			if err != nil {
				return reconcile.Result{}, err
			}
			if !claimed {
				ownedEndpoints = append(ownedEndpoints, endpoint)
			}
		}
		if len(ownedEndpoints) > 0 {
			oldRouter.Spec.Routers = ownedEndpoints
			oldRouter.Spec.Address = ""
			connections, err := connectRouterClients(ctx, r.Client, r.Factory, oldRouter)
			if err != nil {
				return reconcile.Result{}, err
			}
			defer closeRouterConnections(ctx, connections)
			for _, connection := range connections {
				if err := deleteManagedConfiguration(ctx, connection.Client); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
	}
	controllerutil.RemoveFinalizer(obj, resourceFinalizer)
	return reconcile.Result{}, r.Update(ctx, obj)
}

func (r *RouterReconciler) cleanupRemovedEndpoints(ctx context.Context, router api.MikroTikRouter) error {
	current := make(map[string]struct{}, len(routerEndpoints(router)))
	for _, endpoint := range routerEndpoints(router) {
		current[endpointKey(endpoint)] = struct{}{}
	}
	for _, endpoint := range router.Status.AppliedEndpoints {
		if strings.TrimSpace(endpoint.Address) == "" || strings.TrimSpace(endpoint.CredentialsSecret.Name) == "" {
			continue
		}
		if _, exists := current[endpointKey(endpoint)]; exists {
			continue
		}
		claimed, err := endpointClaimedByOtherRouter(ctx, r.Client, router, endpoint)
		if err != nil {
			return err
		}
		if claimed {
			continue
		}
		oldRouter := router
		oldRouter.Spec.Routers = []api.RouterEndpoint{endpoint}
		oldRouter.Spec.Address = ""
		connections, err := connectRouterClients(ctx, r.Client, r.Factory, oldRouter)
		if err != nil {
			return err
		}
		defer closeRouterConnections(ctx, connections)
		for _, connection := range connections {
			err = deleteManagedConfiguration(ctx, connection.Client)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteManagedConfiguration(ctx context.Context, connection ros.Client) error {
	cleaner, ok := connection.(interface {
		DeleteManagedConfiguration(context.Context) error
	})
	if !ok {
		return fmt.Errorf("router client does not support managed configuration cleanup")
	}
	return cleaner.DeleteManagedConfiguration(ctx)
}

func endpointKey(endpoint api.RouterEndpoint) string {
	// Endpoint names and credential secrets are metadata, not the identity of the
	// RouterOS device.  Treating either as identity causes credential rotation or
	// a cosmetic rename to delete every managed entry from the same router.
	port := endpoint.Port
	if port == 0 {
		if endpoint.TLS {
			port = 8729
		} else {
			port = 8728
		}
	}
	address := strings.TrimSpace(endpoint.Address)
	if ip := net.ParseIP(address); ip != nil {
		address = ip.String()
	} else {
		address = strings.ToLower(strings.TrimSuffix(address, "."))
	}
	return strings.Join([]string{address, strconv.FormatInt(int64(port), 10), strconv.FormatBool(endpoint.TLS)}, "|")
}

func durableRouterEndpointUnion(previous, current []api.RouterEndpoint) []api.RouterEndpoint {
	union := make([]api.RouterEndpoint, 0, len(previous)+len(current))
	indexes := make(map[string]int, len(previous)+len(current))
	for _, endpoint := range previous {
		key := endpointKey(endpoint)
		if index, exists := indexes[key]; exists {
			union[index] = endpoint
			continue
		}
		indexes[key] = len(union)
		union = append(union, endpoint)
	}
	for _, endpoint := range current {
		key := endpointKey(endpoint)
		if index, exists := indexes[key]; exists {
			// Keep the current connection metadata (notably credentials) for an
			// unchanged physical endpoint while retaining removed endpoint keys.
			union[index] = endpoint
			continue
		}
		indexes[key] = len(union)
		union = append(union, endpoint)
	}
	return union
}
func (r *RouterReconciler) failRouter(ctx context.Context, o *api.MikroTikRouter, err error) (reconcile.Result, error) {
	oldStatus := o.Status
	o.Status.Connected = false
	o.Status.Conditions = readyCondition(o.Status.Conditions, metav1.ConditionFalse, "ConnectionFailed", err.Error())
	if reflect.DeepEqual(oldStatus, o.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, r.Status().Update(ctx, o)
}
func (r *RouterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&api.MikroTikRouter{}).Complete(r)
}

type DNSReconciler struct {
	client.Client
	Factory ros.Factory
}

const resourceFinalizer = "mikrotik.operator.io/managed-config"
const serviceRouteFinalizer = "mikrotik.operator.io/service-route"
const serviceRouteRouterAnnotation = "mikrotik.operator.io/service-route-router"
const durableRouterTargetsAnnotation = "mikrotik.operator.io/router-targets"

const driftCheckInterval = 1 * time.Minute

var errServiceNotAddressable = errors.New("service is not addressable")
var errGeneratedChildAmbiguity = errors.New("generated child configuration is ambiguous")
var errGeneratedChildCollision = errors.New("generated child conflicts with another owner")
var errGeneratedClaimWaiting = errors.New("waiting for losing generated claim to be removed")
var errImplicitRouterSelection = errors.New("implicit router selection is invalid")

func (d *DNSReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var o api.MikroTikDNSRecord
	if err := d.Get(ctx, req.NamespacedName, &o); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !o.DeletionTimestamp.IsZero() {
		for _, routerRef := range durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef) {
			if err := d.cleanupConfiguration(ctx, &o, routerRef); err != nil {
				return reconcile.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(&o, resourceFinalizer) {
			controllerutil.RemoveFinalizer(&o, resourceFinalizer)
			return reconcile.Result{}, d.Update(ctx, &o)
		}
		return reconcile.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(&o, resourceFinalizer) {
		controllerutil.AddFinalizer(&o, resourceFinalizer)
		if err := d.Update(ctx, &o); err != nil {
			return reconcile.Result{}, err
		}
		// Update changed the resourceVersion. Reconcile again with a fresh
		// object before making external changes or updating status.
		return reconcile.Result{}, nil
	}
	routerKey, err := resolveRouterReference(ctx, d.Client, o.Namespace, o.Spec.RouterRef)
	if err != nil {
		if !errors.Is(err, errImplicitRouterSelection) {
			return reconcile.Result{}, err
		}
		if errors.Is(err, errImplicitRouterSelection) {
			if o.Annotations[durableRouterTargetsAnnotation] == "" && o.Status.RouterRef != "" {
				if _, persistErr := persistDurableRouterTarget(ctx, d.Client, &o, o.Status.RouterRef); persistErr != nil {
					return reconcile.Result{}, persistErr
				}
				return reconcile.Result{}, nil
			}
			if o.Annotations[durableRouterTargetsAnnotation] != "" {
				for _, ref := range durableRouterTargets(&o, o.Status.RouterRef) {
					if cleanupErr := d.cleanupConfiguration(ctx, &o, ref); cleanupErr != nil {
						return d.status(ctx, &o, errors.Join(err, cleanupErr))
					}
				}
				if _, compactErr := compactDurableRouterTarget(ctx, d.Client, &o, ""); compactErr != nil {
					return reconcile.Result{}, compactErr
				}
				return reconcile.Result{}, nil
			}
			if o.Status.RouterRef != "" {
				o.Status.RouterRef = ""
				o.Status.Applied = false
				return reconcile.Result{}, d.Status().Update(ctx, &o)
			}
		}
		return d.status(ctx, &o, err)
	}
	routerRef := routerRefStorage(o.Namespace, routerKey)
	comment := ros.ManagedComment("dns", o.Name, o.Namespace)
	address := o.Spec.Address
	var referencedService *corev1.Service
	if o.Spec.ServiceRef != nil {
		var service corev1.Service
		if err := d.Get(
			ctx,
			types.NamespacedName{Name: o.Spec.ServiceRef.Name, Namespace: o.Spec.ServiceRef.Namespace},
			&service,
		); err != nil {
			if apierrors.IsNotFound(err) {
				for _, ref := range durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef) {
					if cleanupErr := d.cleanupConfiguration(ctx, &o, ref); cleanupErr != nil {
						return d.status(ctx, &o, cleanupErr)
					}
				}
			}
			return d.status(ctx, &o, err)
		}
		referencedService = service.DeepCopy()
		address, err = serviceAddress(ctx, d.Client, service)
		if err != nil {
			if errors.Is(err, errServiceNotAddressable) {
				for _, ref := range durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef) {
					if cleanupErr := d.cleanupConfiguration(ctx, &o, ref); cleanupErr != nil {
						return d.status(ctx, &o, errors.Join(err, cleanupErr))
					}
				}
			}
			return d.status(ctx, &o, err)
		}
	}
	if o.Annotations[durableRouterTargetsAnnotation] != routerRef {
		updated, err := persistDurableRouterTarget(ctx, d.Client, &o, o.Status.RouterRef, routerRef)
		if err != nil {
			return reconcile.Result{}, err
		}
		if updated {
			return reconcile.Result{}, nil
		}
	}
	if o.Annotations[durableRouterTargetsAnnotation] != routerRef {
		if err := cleanupRouterTargets(ctx, d.Client, d.Factory, o.Namespace, durableRouterTargets(&o, o.Status.RouterRef), routerRef, func(ctx context.Context, client ros.Client) error {
			return client.DeleteDNS(ctx, ros.ManagedComment("dns", o.Name, o.Namespace))
		}); err != nil {
			return d.status(ctx, &o, err)
		}
		if _, err := compactDurableRouterTarget(ctx, d.Client, &o, routerRef); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if o.Status.RouterRef != "" && o.Status.RouterRef != routerRef {
		o.Status.RouterRef = ""
		o.Status.Applied = false
		return reconcile.Result{}, d.Status().Update(ctx, &o)
	}
	_, releaseClaim, claimErr := acquireGeneratedChildClaims(
		ctx,
		d.Client,
		&o,
		routerRef,
		"",
		[]generatedDNSCandidate{{
			hostname: o.Spec.Name,
			service:  namespacedNameFromAPI(o.Spec.ServiceRef),
			address:  address,
		}},
		nil,
	)
	if claimErr != nil {
		if errors.Is(claimErr, errGeneratedChildCollision) {
			for _, ref := range durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef) {
				if cleanupErr := d.cleanupConfiguration(ctx, &o, ref); cleanupErr != nil {
					return d.status(ctx, &o, errors.Join(claimErr, cleanupErr))
				}
			}
			return d.status(ctx, &o, claimErr)
		}
		return reconcile.Result{}, claimErr
	}
	defer releaseClaim()
	if err := withRouterConnections(ctx, d.Client, d.Factory, routerKey, true, func(_ api.MikroTikRouter, connections []routerConnection) error {
		for _, connection := range connections {
			if err := connection.Client.EnsureDNS(ctx, o.Spec.Name, address, o.Spec.TTL, comment); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return d.status(ctx, &o, err)
	}
	routeServices := make([]corev1.Service, 0, 1)
	if referencedService != nil && isClusterIPService(*referencedService) && !translatorOwnsGeneratedChildren(&o) {
		routeServices = append(routeServices, *referencedService)
	}
	if err := reconcileOwnedClusterRoutes(ctx, clusterRouteReconcileRequest{
		kube:       d.Client,
		scheme:     d.Scheme(),
		owner:      &o,
		sourceName: "dns/" + o.Name,
		namespace:  o.Namespace,
		routerRef:  routerRef,
		services:   routeServices,
	}); err != nil {
		return d.status(ctx, &o, err)
	}
	oldStatus := o.Status
	o.Status.Applied = true
	o.Status.RouterRef = routerRef
	o.Status.Conditions = readyCondition(o.Status.Conditions, metav1.ConditionTrue, "Applied", "DNS record applied")
	if reflect.DeepEqual(oldStatus, o.Status) {
		return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, d.Status().Update(ctx, &o)
}

func (d *DNSReconciler) cleanupConfiguration(ctx context.Context, o *api.MikroTikDNSRecord, routerRef string) error {
	if err := reconcileOwnedClusterRoutes(ctx, clusterRouteReconcileRequest{
		kube:       d.Client,
		scheme:     d.Scheme(),
		owner:      o,
		sourceName: "dns/" + o.Name,
		namespace:  o.Namespace,
		routerRef:  routerRef,
	}); err != nil {
		return err
	}
	err := withRouterConnections(ctx, d.Client, d.Factory, routerKeyFromRef(o.Namespace, routerRef), false, func(_ api.MikroTikRouter, connections []routerConnection) error {
		for _, connection := range connections {
			if err := connection.Client.DeleteDNS(ctx, ros.ManagedComment("dns", o.Name, o.Namespace)); err != nil {
				return err
			}
		}
		return nil
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
func (d *DNSReconciler) status(ctx context.Context, o *api.MikroTikDNSRecord, err error) (reconcile.Result, error) {
	oldStatus := o.Status
	o.Status.Applied = false
	o.Status.Conditions = readyCondition(o.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", err.Error())
	if reflect.DeepEqual(oldStatus, o.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, d.Status().Update(ctx, o)
}
func (d *DNSReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.MikroTikDNSRecord{}).
		Owns(&api.MikroTikRoute{}).
		Complete(d)
}

func splitRouterReference(reference string) (namespace, name string) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", ""
	}
	namespace, name, found := strings.Cut(reference, "/")
	if !found {
		return "", reference
	}
	return namespace, name
}

func routerRefStorage(resourceNamespace string, key types.NamespacedName) string {
	if key.Namespace == "" || key.Namespace == resourceNamespace {
		return key.Name
	}
	return key.Namespace + "/" + key.Name
}

func routerKeyFromRef(resourceNamespace, reference string) types.NamespacedName {
	hintNamespace, name := splitRouterReference(reference)
	if name == "" {
		return types.NamespacedName{}
	}
	if hintNamespace == "" {
		hintNamespace = resourceNamespace
	}
	return types.NamespacedName{Namespace: hintNamespace, Name: name}
}

func canonicalRouterClaimKey(key types.NamespacedName) string {
	if key.Name == "" {
		return ""
	}
	return key.Namespace + "/" + key.Name
}

func resolveRouterReference(ctx context.Context, kube client.Client, namespace, reference string) (types.NamespacedName, error) {
	hintNamespace, name := splitRouterReference(reference)
	if name != "" && hintNamespace != "" {
		key := types.NamespacedName{Namespace: hintNamespace, Name: name}
		if _, err := getMikroTikRouter(ctx, kube, key); err != nil {
			return types.NamespacedName{}, err
		}
		return key, nil
	}
	if name != "" {
		local := types.NamespacedName{Namespace: namespace, Name: name}
		_, err := getMikroTikRouter(ctx, kube, local)
		if err == nil {
			return local, nil
		}
		if !apierrors.IsNotFound(err) {
			return types.NamespacedName{}, err
		}
		cluster, clusterErr := uniqueClusterRouter(ctx, kube, name)
		if clusterErr == nil {
			return cluster, nil
		}
		if apierrors.IsNotFound(clusterErr) {
			return local, nil
		}
		return types.NamespacedName{}, clusterErr
	}
	var localRouters api.MikroTikRouterList
	if err := kube.List(ctx, &localRouters, client.InNamespace(namespace)); err != nil {
		return types.NamespacedName{}, err
	}
	live := liveRouters(localRouters.Items)
	if len(live) == 1 {
		router := live[0]
		return types.NamespacedName{Namespace: router.Namespace, Name: router.Name}, nil
	}
	if len(live) > 1 {
		return types.NamespacedName{}, fmt.Errorf(
			"%w: multiple MikroTikRouters exist in namespace %s; set routerRef explicitly",
			errImplicitRouterSelection,
			namespace,
		)
	}
	return uniqueClusterRouter(ctx, kube, "")
}

func uniqueClusterRouter(ctx context.Context, kube client.Client, name string) (types.NamespacedName, error) {
	var routers api.MikroTikRouterList
	if err := kube.List(ctx, &routers); err != nil {
		return types.NamespacedName{}, err
	}
	matches := make([]api.MikroTikRouter, 0, len(routers.Items))
	for _, router := range liveRouters(routers.Items) {
		if name != "" && router.Name != name {
			continue
		}
		matches = append(matches, router)
	}
	if len(matches) == 1 {
		router := matches[0]
		return types.NamespacedName{Namespace: router.Namespace, Name: router.Name}, nil
	}
	if len(matches) == 0 {
		if name != "" {
			return types.NamespacedName{}, apierrors.NewNotFound(
				api.GroupVersion.WithResource("mikrotikrouters").GroupResource(),
				name,
			)
		}
		return types.NamespacedName{}, fmt.Errorf("%w: no MikroTikRouter exists in the cluster", errImplicitRouterSelection)
	}
	return types.NamespacedName{}, fmt.Errorf("%w: multiple MikroTikRouters exist; set routerRef explicitly", errImplicitRouterSelection)
}

func liveRouters(routers []api.MikroTikRouter) []api.MikroTikRouter {
	live := make([]api.MikroTikRouter, 0, len(routers))
	for _, router := range routers {
		if router.DeletionTimestamp.IsZero() {
			live = append(live, router)
		}
	}
	return live
}

func serviceAddress(ctx context.Context, kube client.Client, service corev1.Service) (string, error) {
	if service.Spec.Type != corev1.ServiceTypeNodePort {
		if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
			return "", fmt.Errorf("%w: service %s/%s has no ClusterIP", errServiceNotAddressable, service.Namespace, service.Name)
		}
		return service.Spec.ClusterIP, nil
	}
	var nodes corev1.NodeList
	if err := kube.List(ctx, &nodes); err != nil {
		return "", err
	}
	addresses := nodeInternalIPs(nodes.Items)
	if len(addresses) == 0 {
		return "", fmt.Errorf("%w: no node InternalIP found for NodePort service %s/%s", errServiceNotAddressable, service.Namespace, service.Name)
	}
	return addresses[0], nil
}

// nodeInternalIPs returns unique node InternalIPs in stable sort order so
// NodePort NAT/DNS and single-node routes do not flap when List order changes.
func nodeInternalIPs(nodes []corev1.Node) []string {
	addresses := make([]string, 0, len(nodes))
	for _, node := range nodes {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP && address.Address != "" {
				addresses = append(addresses, address.Address)
				break
			}
		}
	}
	sort.Strings(addresses)
	unique := addresses[:0]
	for _, address := range addresses {
		if len(unique) == 0 || unique[len(unique)-1] != address {
			unique = append(unique, address)
		}
	}
	return unique
}

type ServiceDNSReconciler struct {
	client.Client
	Factory       ros.Factory
	RuntimeScheme *runtime.Scheme
}

type portForwardReconcileRequest struct {
	kube                 client.Client
	scheme               *runtime.Scheme
	owner                client.Object
	sourceName           string
	namespace            string
	publicIP             string
	routerRef            string
	services             []corev1.Service
	servicePorts         map[types.NamespacedName][]corev1.ServicePort
	requireSelectedPorts bool
	prepared             bool
	candidates           []portForwardCandidate
}

type portForwardCandidate struct {
	name          string
	service       types.NamespacedName
	protocol      string
	externalPort  int32
	targetAddress string
	targetPort    int32
}

func (s *ServiceDNSReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var service corev1.Service
	if err := s.Get(ctx, req.NamespacedName, &service); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, s.cleanupDeletedServiceRoutes(ctx, req.NamespacedName)
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !service.DeletionTimestamp.IsZero() {
		return s.reconcileServiceDeletion(ctx, &service)
	}
	dnsName := service.Annotations[api.DNSNameAnnotation]
	portForwardRequest, err := preparePortForwardReconcileRequest(ctx, portForwardReconcileRequest{
		kube:       s.Client,
		scheme:     s.RuntimeScheme,
		owner:      &service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
		publicIP:   service.Annotations[api.PublicIPAnnotation],
		routerRef:  service.Annotations[api.RouterRefAnnotation],
		services:   []corev1.Service{service},
	})
	if err != nil {
		if errors.Is(err, errGeneratedChildAmbiguity) || errors.Is(err, errImplicitRouterSelection) {
			err = s.cleanupGeneratedChildren(ctx, &service, err)
		}
		return reconcile.Result{}, err
	}
	if dnsName == "" {
		resolvedRouter, releaseClaims, err := acquireGeneratedChildClaims(
			ctx,
			s.Client,
			&service,
			portForwardRequest.routerRef,
			portForwardRequest.publicIP,
			nil,
			portForwardRequest.candidates,
		)
		if err != nil {
			if errors.Is(err, errGeneratedChildCollision) || errors.Is(err, errImplicitRouterSelection) {
				err = s.cleanupGeneratedChildren(ctx, &service, err)
			}
			return reconcile.Result{}, err
		}
		defer releaseClaims()
		portForwardRequest.routerRef = resolvedRouter
		if err := reconcileServicePortForwards(ctx, portForwardRequest); err != nil {
			return reconcile.Result{}, err
		}
		if err := s.reconcileServiceClusterRoutes(ctx, &service, resolvedRouter); err != nil {
			return reconcile.Result{}, err
		}
		name := service.Name + "-dns"
		if len(name) > 63 {
			name = name[:63]
		}
		var record api.MikroTikDNSRecord
		if err := s.Get(ctx, types.NamespacedName{Name: name, Namespace: service.Namespace}, &record); err == nil {
			if metav1.IsControlledBy(&record, &service) {
				if err := s.Delete(ctx, &record); err != nil {
					return reconcile.Result{}, err
				}
				if controllerutil.ContainsFinalizer(&service, serviceRouteFinalizer) {
					controllerutil.RemoveFinalizer(&service, serviceRouteFinalizer)
					delete(service.Annotations, serviceRouteRouterAnnotation)
					return reconcile.Result{}, s.Update(ctx, &service)
				}
				if updated, err := compactServiceRouteRouterTarget(ctx, s.Client, &service, ""); err != nil {
					return reconcile.Result{}, err
				} else if updated {
					return reconcile.Result{}, nil
				}
				return reconcile.Result{}, nil
			}
		} else if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		if controllerutil.ContainsFinalizer(&service, serviceRouteFinalizer) {
			controllerutil.RemoveFinalizer(&service, serviceRouteFinalizer)
			delete(service.Annotations, serviceRouteRouterAnnotation)
			return reconcile.Result{}, s.Update(ctx, &service)
		}
		if updated, err := compactServiceRouteRouterTarget(ctx, s.Client, &service, ""); err != nil {
			return reconcile.Result{}, err
		} else if updated {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, nil
	}
	routerKey, err := s.resolveRouterRef(ctx, service)
	if err != nil {
		return reconcile.Result{}, err
	}
	routerRef := routerRefStorage(service.Namespace, routerKey)
	address := service.Spec.ClusterIP
	if service.Spec.Type == corev1.ServiceTypeNodePort {
		var nodes corev1.NodeList
		if err := s.List(ctx, &nodes); err != nil {
			return reconcile.Result{}, err
		}
		address = ""
		for _, node := range nodes.Items {
			for _, nodeAddress := range node.Status.Addresses {
				if nodeAddress.Type == corev1.NodeInternalIP && nodeAddress.Address != "" {
					address = nodeAddress.Address
					break
				}
			}
			if address != "" {
				break
			}
		}
	}
	if address == "" || address == corev1.ClusterIPNone {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	name := service.Name + "-dns"
	if len(name) > 63 {
		name = name[:63]
	}
	resolvedRouter, releaseClaims, err := acquireGeneratedChildClaims(
		ctx,
		s.Client,
		&service,
		routerRef,
		portForwardRequest.publicIP,
		[]generatedDNSCandidate{{
			childName: name,
			hostname:  dnsName,
			service:   req.NamespacedName,
			address:   address,
		}},
		portForwardRequest.candidates,
	)
	if err != nil {
		if errors.Is(err, errGeneratedChildCollision) || errors.Is(err, errImplicitRouterSelection) {
			err = s.cleanupGeneratedChildren(ctx, &service, err)
		}
		return reconcile.Result{}, err
	}
	defer releaseClaims()
	routerRef = resolvedRouter
	portForwardRequest.routerRef = resolvedRouter
	if err := reconcileServicePortForwards(ctx, portForwardRequest); err != nil {
		return reconcile.Result{}, err
	}
	if err := s.reconcileServiceClusterRoutes(ctx, &service, routerRef); err != nil {
		return reconcile.Result{}, err
	}
	var record api.MikroTikDNSRecord
	key := types.NamespacedName{Name: name, Namespace: service.Namespace}
	err = s.Get(ctx, key, &record)
	if apierrors.IsNotFound(err) {
		record = api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: service.Namespace}}
		if err := controllerutil.SetControllerReference(&service, &record, s.Scheme()); err != nil {
			return reconcile.Result{}, err
		}
		record.Spec = api.MikroTikDNSRecordSpec{RouterRef: routerRef, Name: dnsName, Address: address}
		return reconcile.Result{}, s.Create(ctx, &record)
	}
	if err != nil {
		return reconcile.Result{}, err
	}
	if !metav1.IsControlledBy(&record, &service) {
		return reconcile.Result{}, fmt.Errorf("DNS record %s/%s already exists and is not owned by Service %s/%s", record.Namespace, record.Name, service.Namespace, service.Name)
	}
	if record.Spec.RouterRef != routerRef || record.Spec.Name != dnsName || record.Spec.Address != address {
		record.Spec.RouterRef = routerRef
		record.Spec.Name = dnsName
		record.Spec.Address = address
		return reconcile.Result{}, s.Update(ctx, &record)
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
}

func (s *ServiceDNSReconciler) cleanupGeneratedChildren(ctx context.Context, service *corev1.Service, cause error) error {
	name := service.Name + "-dns"
	if len(name) > 63 {
		name = name[:63]
	}
	var cleanupErrors []error
	var record api.MikroTikDNSRecord
	if err := s.Get(ctx, types.NamespacedName{Name: name, Namespace: service.Namespace}, &record); err == nil {
		if metav1.IsControlledBy(&record, service) {
			if err := s.Delete(ctx, &record); err != nil && !apierrors.IsNotFound(err) {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
	} else if !apierrors.IsNotFound(err) {
		cleanupErrors = append(cleanupErrors, err)
	}
	if err := reconcileServicePortForwards(ctx, portForwardReconcileRequest{
		kube:       s.Client,
		scheme:     s.RuntimeScheme,
		owner:      service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
	}); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	if err := s.deleteOwnedServiceClusterRoutes(ctx, service); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(append([]error{cause}, cleanupErrors...)...)
}

func (s *ServiceDNSReconciler) reconcileServiceDeletion(ctx context.Context, service *corev1.Service) (reconcile.Result, error) {
	routeErr := s.deleteOwnedServiceClusterRoutes(ctx, service)
	if !controllerutil.ContainsFinalizer(service, serviceRouteFinalizer) {
		return reconcile.Result{}, routeErr
	}
	controllerutil.RemoveFinalizer(service, serviceRouteFinalizer)
	delete(service.Annotations, serviceRouteRouterAnnotation)
	if err := s.Update(ctx, service); err != nil {
		return reconcile.Result{}, errors.Join(routeErr, err)
	}
	return reconcile.Result{}, routeErr
}

func serviceRouteRouterRefs(service corev1.Service, record *api.MikroTikDNSRecord) []string {
	seen := make(map[string]struct{})
	refs := make([]string, 0, 3)
	add := func(ref string) {
		if ref == "" {
			return
		}
		if _, exists := seen[ref]; exists {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	for _, ref := range strings.Split(service.Annotations[serviceRouteRouterAnnotation], ",") {
		add(ref)
	}
	if record != nil {
		add(record.Status.RouterRef)
		add(record.Spec.RouterRef)
	}
	return refs
}

func persistServiceRouteRouterTarget(ctx context.Context, kube client.Client, service *corev1.Service, routerRef string) (bool, error) {
	refs := serviceRouteRouterRefs(*service, nil)
	if slices.Contains(refs, routerRef) {
		return false, nil
	}
	refs = append(refs, routerRef)
	sort.Strings(refs)
	if service.Annotations == nil {
		service.Annotations = make(map[string]string)
	}
	service.Annotations[serviceRouteRouterAnnotation] = strings.Join(refs, ",")
	return true, kube.Update(ctx, service)
}

func compactServiceRouteRouterTarget(ctx context.Context, kube client.Client, service *corev1.Service, routerRef string) (bool, error) {
	annotations := make(map[string]string, len(service.Annotations))
	for key, value := range service.Annotations {
		annotations[key] = value
	}
	if routerRef == "" {
		if _, exists := annotations[serviceRouteRouterAnnotation]; !exists {
			return false, nil
		}
		delete(annotations, serviceRouteRouterAnnotation)
	} else {
		if annotations[serviceRouteRouterAnnotation] == routerRef {
			return false, nil
		}
		annotations[serviceRouteRouterAnnotation] = routerRef
	}
	service.Annotations = annotations
	return true, kube.Update(ctx, service)
}

func (s *ServiceDNSReconciler) cleanupDeletedServiceRoutes(ctx context.Context, key types.NamespacedName) error {
	return deleteLabeledClusterRoutes(ctx, s.Client, key.Namespace, "service/"+key.Name)
}

func (s *ServiceDNSReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Owns(&api.MikroTikDNSRecord{}).
		Owns(&api.MikroTikPortForward{}).
		Owns(&api.MikroTikRoute{}).
		Complete(s)
}

type IngressReconciler struct {
	client.Client
	Factory       ros.Factory
	RuntimeScheme *runtime.Scheme
}

func (i *IngressReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var ingress networkingv1.Ingress
	if err := i.Get(ctx, req.NamespacedName, &ingress); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if ingress.Spec.IngressClassName == nil || *ingress.Spec.IngressClassName != api.IngressClassName {
		if err := cleanupOwnedChildren(ctx, i.Client, i.RuntimeScheme, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	var ingressClass networkingv1.IngressClass
	if err := i.Get(ctx, types.NamespacedName{Name: api.IngressClassName}, &ingressClass); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		if cleanupErr := cleanupOwnedChildren(ctx, i.Client, i.RuntimeScheme, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name); cleanupErr != nil {
			return reconcile.Result{}, errors.Join(err, cleanupErr)
		}
		return reconcile.Result{}, err
	}
	if ingressClass.Spec.Controller != api.IngressController {
		if err := cleanupOwnedChildren(ctx, i.Client, i.RuntimeScheme, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	observedServices, backendErrors, err := i.observeBackendServices(ctx, ingress)
	if err != nil {
		return reconcile.Result{}, err
	}
	services := make([]corev1.Service, 0)
	servicePorts := make(map[types.NamespacedName][]corev1.ServicePort)
	dnsCandidates := make([]generatedDNSCandidate, 0)
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil || path.Backend.Service.Name == "" {
				continue
			}
			service, exists := observedServices[types.NamespacedName{Name: path.Backend.Service.Name, Namespace: ingress.Namespace}]
			if !exists {
				continue
			}
			port, ok := findIngressServicePort(service, path.Backend.Service.Port)
			if !ok {
				backendErrors = append(backendErrors, fmt.Errorf("ingress %s/%s references unavailable port on Service %s/%s", ingress.Namespace, ingress.Name, service.Namespace, service.Name))
				continue
			}
			services = appendUniqueService(services, service)
			if rule.Host != "" {
				dnsCandidates = append(dnsCandidates, generatedDNSCandidate{
					childName: "ing-" + shortHash(ingress.Name+"/"+rule.Host+"/"+service.Name),
					hostname:  rule.Host,
					service:   types.NamespacedName{Namespace: service.Namespace, Name: service.Name},
					address:   service.Spec.ClusterIP,
				})
			}
			key := types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
			servicePorts[key] = appendUniqueServicePort(servicePorts[key], port)
		}
	}
	portForwardRequest, err := preparePortForwardReconcileRequest(ctx, portForwardReconcileRequest{
		kube:                 i.Client,
		scheme:               i.RuntimeScheme,
		owner:                &ingress,
		sourceName:           "ingress/" + ingress.Name,
		namespace:            ingress.Namespace,
		publicIP:             ingress.Annotations[api.PublicIPAnnotation],
		routerRef:            ingress.Annotations[api.RouterRefAnnotation],
		services:             services,
		servicePorts:         servicePorts,
		requireSelectedPorts: true,
	})
	if err != nil {
		if errors.Is(err, errGeneratedChildAmbiguity) || errors.Is(err, errImplicitRouterSelection) {
			err = cleanupAmbiguousGeneratedChildren(ctx, i.Client, i.RuntimeScheme, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name, err)
		}
		return reconcile.Result{}, err
	}
	if err := validateGeneratedDNSCandidates("Ingress "+ingress.Namespace+"/"+ingress.Name, dnsCandidates); err != nil {
		return reconcile.Result{}, cleanupAmbiguousGeneratedChildren(ctx, i.Client, i.RuntimeScheme, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name, err)
	}
	resolvedRouter, releaseClaims, err := acquireGeneratedChildClaims(
		ctx,
		i.Client,
		&ingress,
		portForwardRequest.routerRef,
		portForwardRequest.publicIP,
		dnsCandidates,
		portForwardRequest.candidates,
	)
	if err != nil {
		if errors.Is(err, errGeneratedChildCollision) || errors.Is(err, errImplicitRouterSelection) {
			err = cleanupAmbiguousGeneratedChildren(ctx, i.Client, i.RuntimeScheme, &ingress, "ingress", ingress.Name, "ingress/"+ingress.Name, err)
		}
		return reconcile.Result{}, err
	}
	defer releaseClaims()
	portForwardRequest.routerRef = resolvedRouter
	labelValue := ingress.Name
	var existing api.MikroTikDNSRecordList
	if err := i.List(ctx, &existing, client.InNamespace(ingress.Namespace)); err != nil {
		return reconcile.Result{}, err
	}
	desired := map[string]bool{}
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" || rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil || path.Backend.Service.Name == "" {
				continue
			}
			service, exists := observedServices[types.NamespacedName{Name: path.Backend.Service.Name, Namespace: ingress.Namespace}]
			if !exists {
				continue
			}
			if _, validPort := findIngressServicePort(service, path.Backend.Service.Port); !validPort {
				continue
			}
			if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
				continue
			}
			name := "ing-" + shortHash(ingress.Name+"/"+rule.Host+"/"+path.Backend.Service.Name)
			desired[name] = true
			var record api.MikroTikDNSRecord
			err := i.Get(ctx, types.NamespacedName{Name: name, Namespace: ingress.Namespace}, &record)
			if apierrors.IsNotFound(err) {
				record = api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ingress.Namespace, Labels: map[string]string{"mikrotik.operator.io/ingress": labelValue}}}
				if err := controllerutil.SetControllerReference(&ingress, &record, i.Scheme()); err != nil {
					return reconcile.Result{}, err
				}
				record.Spec = api.MikroTikDNSRecordSpec{RouterRef: resolvedRouter, Name: rule.Host, Address: service.Spec.ClusterIP, ServiceRef: &api.NamespacedName{Namespace: ingress.Namespace, Name: service.Name}}
				if err := i.Create(ctx, &record); err != nil {
					return reconcile.Result{}, err
				}
			} else if err != nil {
				return reconcile.Result{}, err
			} else if !metav1.IsControlledBy(&record, &ingress) {
				return reconcile.Result{}, fmt.Errorf("DNS record %s/%s already exists and is not owned by Ingress %s/%s", record.Namespace, record.Name, ingress.Namespace, ingress.Name)
			} else if record.Spec.RouterRef != resolvedRouter || record.Spec.Name != rule.Host || record.Spec.Address != service.Spec.ClusterIP || record.Spec.ServiceRef == nil || record.Spec.ServiceRef.Name != service.Name || record.Spec.ServiceRef.Namespace != service.Namespace {
				record.Spec.RouterRef = resolvedRouter
				record.Spec.Name = rule.Host
				record.Spec.Address = service.Spec.ClusterIP
				record.Spec.ServiceRef = &api.NamespacedName{Namespace: service.Namespace, Name: service.Name}
				if err := i.Update(ctx, &record); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
	}
	for _, record := range existing.Items {
		if !desired[record.Name] && metav1.IsControlledBy(&record, &ingress) {
			if err := i.Delete(ctx, &record); err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	if err := reconcileServicePortForwards(ctx, portForwardRequest); err != nil {
		return reconcile.Result{}, err
	}
	if err := reconcileOwnedClusterRoutes(ctx, clusterRouteReconcileRequest{
		kube:       i.Client,
		scheme:     i.RuntimeScheme,
		owner:      &ingress,
		sourceName: "ingress/" + ingress.Name,
		namespace:  ingress.Namespace,
		routerRef:  resolvedRouter,
		services:   services,
	}); err != nil {
		return reconcile.Result{}, err
	}
	if err := errors.Join(backendErrors...); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
}

func (i *IngressReconciler) observeBackendServices(ctx context.Context, ingress networkingv1.Ingress) (map[types.NamespacedName]corev1.Service, []error, error) {
	services := make(map[types.NamespacedName]corev1.Service)
	var semanticErrors []error
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil || path.Backend.Service.Name == "" {
				continue
			}
			key := types.NamespacedName{Namespace: ingress.Namespace, Name: path.Backend.Service.Name}
			if _, observed := services[key]; observed {
				continue
			}
			var service corev1.Service
			if err := i.Get(ctx, key, &service); err != nil {
				if apierrors.IsNotFound(err) {
					semanticErrors = append(semanticErrors, err)
					continue
				}
				return nil, nil, err
			}
			services[key] = service
		}
	}
	return services, semanticErrors, nil
}

func findIngressServicePort(service corev1.Service, port networkingv1.ServiceBackendPort) (corev1.ServicePort, bool) {
	for _, candidate := range service.Spec.Ports {
		if port.Name != "" && candidate.Name == port.Name {
			return candidate, true
		}
		if port.Number != 0 && candidate.Port == port.Number {
			return candidate, true
		}
	}
	return corev1.ServicePort{}, false
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func cleanupOwnedChildren(
	ctx context.Context,
	kube client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	_ string,
	_ string,
	sourceName string,
) error {
	var records api.MikroTikDNSRecordList
	if err := kube.List(ctx, &records, client.InNamespace(owner.GetNamespace())); err != nil {
		return err
	}
	for index := range records.Items {
		if !metav1.IsControlledBy(&records.Items[index], owner) {
			continue
		}
		if err := kube.Delete(ctx, &records.Items[index]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	var routes api.MikroTikRouteList
	if err := kube.List(ctx, &routes, client.InNamespace(owner.GetNamespace())); err != nil {
		return err
	}
	for index := range routes.Items {
		if !metav1.IsControlledBy(&routes.Items[index], owner) {
			continue
		}
		if err := kube.Delete(ctx, &routes.Items[index]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return reconcileServicePortForwards(ctx, portForwardReconcileRequest{
		kube:       kube,
		scheme:     scheme,
		owner:      owner,
		sourceName: sourceName,
		namespace:  owner.GetNamespace(),
	})
}

func (i *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&api.MikroTikDNSRecord{}).
		Owns(&api.MikroTikPortForward{}).
		Owns(&api.MikroTikRoute{}).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(i.ingressesInNamespace)).
		Watches(&networkingv1.IngressClass{}, handler.EnqueueRequestsFromMapFunc(i.ingressesInNamespace)).
		Complete(i)
}

func (i *IngressReconciler) ingressesInNamespace(ctx context.Context, object client.Object) []reconcile.Request {
	var ingresses networkingv1.IngressList
	listOptions := []client.ListOption{}
	if namespace := object.GetNamespace(); namespace != "" {
		listOptions = append(listOptions, client.InNamespace(namespace))
	}
	if err := i.List(ctx, &ingresses, listOptions...); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(ingresses.Items))
	for _, ingress := range ingresses.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}})
	}
	return requests
}

type HTTPRouteReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	GatewayClass   string
	ControllerName string
}

func (h *HTTPRouteReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var route gatewayv1.HTTPRoute
	if err := h.Get(ctx, req.NamespacedName, &route); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	acceptedHostnames, attached, err := h.acceptedHostnamesForMikroTikGateway(ctx, route)
	if err != nil {
		return reconcile.Result{}, err
	}
	if !attached {
		if err := cleanupOwnedChildren(ctx, h.Client, h.Scheme, &route, "httproute", route.Name, "httproute/"+route.Name); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
	}
	allowedCrossNamespaceBackends, err := h.allowedCrossNamespaceBackends(ctx, route)
	if err != nil {
		return reconcile.Result{}, err
	}
	observedServices, backendErrors, err := h.observeBackendServices(ctx, route, allowedCrossNamespaceBackends)
	if err != nil {
		return reconcile.Result{}, err
	}
	services := make([]corev1.Service, 0)
	servicePorts := make(map[types.NamespacedName][]corev1.ServicePort)
	dnsCandidates := make([]generatedDNSCandidate, 0)
	for _, rule := range route.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			if !isServiceBackend(backend) {
				continue
			}
			serviceNamespace := route.Namespace
			if backend.Namespace != nil {
				serviceNamespace = string(*backend.Namespace)
			}
			key := types.NamespacedName{Namespace: serviceNamespace, Name: string(backend.Name)}
			if serviceNamespace != route.Namespace && !allowedCrossNamespaceBackends[key] {
				continue
			}
			service, exists := observedServices[key]
			if !exists {
				continue
			}
			if backend.Port == nil {
				backendErrors = append(backendErrors, fmt.Errorf("HTTPRoute %s/%s backend for Service %s/%s has no port", route.Namespace, route.Name, service.Namespace, service.Name))
				continue
			}
			port, ok := findServicePort(service, int32(*backend.Port))
			if !ok {
				backendErrors = append(backendErrors, fmt.Errorf("HTTPRoute %s/%s references unavailable port on Service %s/%s", route.Namespace, route.Name, service.Namespace, service.Name))
				continue
			}
			services = appendUniqueService(services, service)
			for _, hostname := range acceptedHostnames {
				dnsCandidates = append(dnsCandidates, generatedDNSCandidate{
					childName: "httproute-" + shortHash(route.Namespace+"/"+route.Name+"/"+string(hostname)+"/"+serviceNamespace+"/"+service.Name),
					hostname:  string(hostname),
					service:   key,
					address:   service.Spec.ClusterIP,
				})
			}
			servicePorts[key] = appendUniqueServicePort(servicePorts[key], port)
		}
	}
	portForwardRequest, err := preparePortForwardReconcileRequest(ctx, portForwardReconcileRequest{
		kube:                 h.Client,
		scheme:               h.Scheme,
		owner:                &route,
		sourceName:           "httproute/" + route.Name,
		namespace:            route.Namespace,
		publicIP:             route.Annotations[api.PublicIPAnnotation],
		routerRef:            route.Annotations[api.RouterRefAnnotation],
		services:             services,
		servicePorts:         servicePorts,
		requireSelectedPorts: true,
	})
	if err != nil {
		if errors.Is(err, errGeneratedChildAmbiguity) || errors.Is(err, errImplicitRouterSelection) {
			err = cleanupAmbiguousGeneratedChildren(ctx, h.Client, h.Scheme, &route, "httproute", route.Name, "httproute/"+route.Name, err)
		}
		return reconcile.Result{}, err
	}
	if err := validateGeneratedDNSCandidates("HTTPRoute "+route.Namespace+"/"+route.Name, dnsCandidates); err != nil {
		return reconcile.Result{}, cleanupAmbiguousGeneratedChildren(ctx, h.Client, h.Scheme, &route, "httproute", route.Name, "httproute/"+route.Name, err)
	}
	resolvedRouter, releaseClaims, err := acquireGeneratedChildClaims(
		ctx,
		h.Client,
		&route,
		portForwardRequest.routerRef,
		portForwardRequest.publicIP,
		dnsCandidates,
		portForwardRequest.candidates,
	)
	if err != nil {
		if errors.Is(err, errGeneratedChildCollision) || errors.Is(err, errImplicitRouterSelection) {
			err = cleanupAmbiguousGeneratedChildren(ctx, h.Client, h.Scheme, &route, "httproute", route.Name, "httproute/"+route.Name, err)
		}
		return reconcile.Result{}, err
	}
	defer releaseClaims()
	portForwardRequest.routerRef = resolvedRouter

	labelValue := route.Name
	var existing api.MikroTikDNSRecordList
	if err := h.List(ctx, &existing, client.InNamespace(route.Namespace)); err != nil {
		return reconcile.Result{}, err
	}
	desired := map[string]bool{}
	for _, hostname := range acceptedHostnames {
		if hostname == "" {
			continue
		}
		for _, rule := range route.Spec.Rules {
			for _, backend := range rule.BackendRefs {
				if !isServiceBackend(backend) {
					continue
				}
				serviceName := string(backend.Name)
				serviceNamespace := route.Namespace
				if backend.Namespace != nil {
					serviceNamespace = string(*backend.Namespace)
				}
				if serviceNamespace != route.Namespace && !allowedCrossNamespaceBackends[types.NamespacedName{Namespace: serviceNamespace, Name: serviceName}] {
					continue
				}
				service, exists := observedServices[types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}]
				if !exists {
					continue
				}
				if backend.Port == nil {
					continue
				}
				if _, validPort := findServicePort(service, int32(*backend.Port)); !validPort {
					continue
				}
				if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
					continue
				}
				name := "httproute-" + shortHash(route.Namespace+"/"+route.Name+"/"+string(hostname)+"/"+serviceNamespace+"/"+serviceName)
				desired[name] = true
				var record api.MikroTikDNSRecord
				err := h.Get(ctx, types.NamespacedName{Name: name, Namespace: route.Namespace}, &record)
				if apierrors.IsNotFound(err) {
					record = api.MikroTikDNSRecord{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: route.Namespace, Labels: map[string]string{"mikrotik.operator.io/httproute": labelValue}}}
					if err := controllerutil.SetControllerReference(&route, &record, h.Scheme); err != nil {
						return reconcile.Result{}, err
					}
					record.Spec = api.MikroTikDNSRecordSpec{RouterRef: resolvedRouter, Name: string(hostname), Address: service.Spec.ClusterIP, ServiceRef: &api.NamespacedName{Namespace: serviceNamespace, Name: serviceName}}
					if err := h.Create(ctx, &record); err != nil {
						return reconcile.Result{}, err
					}
				} else if err != nil {
					return reconcile.Result{}, err
				} else if !metav1.IsControlledBy(&record, &route) {
					return reconcile.Result{}, fmt.Errorf("DNS record %s/%s is not owned by HTTPRoute %s/%s", record.Namespace, record.Name, route.Namespace, route.Name)
				} else if record.Spec.RouterRef != resolvedRouter || record.Spec.Name != string(hostname) || record.Spec.Address != service.Spec.ClusterIP || record.Spec.ServiceRef == nil || record.Spec.ServiceRef.Name != serviceName || record.Spec.ServiceRef.Namespace != serviceNamespace {
					record.Spec.RouterRef = resolvedRouter
					record.Spec.Name = string(hostname)
					record.Spec.Address = service.Spec.ClusterIP
					record.Spec.ServiceRef = &api.NamespacedName{Namespace: serviceNamespace, Name: serviceName}
					if err := h.Update(ctx, &record); err != nil {
						return reconcile.Result{}, err
					}
				}
			}
		}
	}
	for _, record := range existing.Items {
		if !desired[record.Name] && metav1.IsControlledBy(&record, &route) {
			if err := h.Delete(ctx, &record); err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	if err := reconcileServicePortForwards(ctx, portForwardRequest); err != nil {
		return reconcile.Result{}, err
	}
	if err := reconcileOwnedClusterRoutes(ctx, clusterRouteReconcileRequest{
		kube:       h.Client,
		scheme:     h.Scheme,
		owner:      &route,
		sourceName: "httproute/" + route.Name,
		namespace:  route.Namespace,
		routerRef:  resolvedRouter,
		services:   services,
	}); err != nil {
		return reconcile.Result{}, err
	}
	if err := errors.Join(backendErrors...); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
}

func isServiceBackend(backend gatewayv1.HTTPBackendRef) bool {
	if backend.Group != nil && string(*backend.Group) != "" {
		return false
	}
	if backend.Kind != nil && string(*backend.Kind) != "Service" {
		return false
	}
	return backend.Name != ""
}

func (h *HTTPRouteReconciler) acceptedHostnamesForMikroTikGateway(ctx context.Context, route gatewayv1.HTTPRoute) ([]gatewayv1.Hostname, bool, error) {
	gatewayClassName := h.GatewayClass
	if gatewayClassName == "" {
		gatewayClassName = api.GatewayClassName
	}
	controllerName := h.ControllerName
	if controllerName == "" {
		controllerName = api.GatewayController
	}
	accepted := make(map[gatewayv1.Hostname]struct{})
	attached := false
	for _, parent := range route.Spec.ParentRefs {
		if parent.Group != nil && string(*parent.Group) != gatewayv1.GroupVersion.Group {
			continue
		}
		if parent.Kind != nil && string(*parent.Kind) != "Gateway" {
			continue
		}
		gatewayNamespace := route.Namespace
		if parent.Namespace != nil {
			gatewayNamespace = string(*parent.Namespace)
		}
		var gateway gatewayv1.Gateway
		if err := h.Get(ctx, types.NamespacedName{Name: string(parent.Name), Namespace: gatewayNamespace}, &gateway); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, false, err
			}
			continue
		}
		if string(gateway.Spec.GatewayClassName) != gatewayClassName {
			continue
		}
		var gatewayClass gatewayv1.GatewayClass
		if err := h.Get(ctx, types.NamespacedName{Name: gatewayClassName}, &gatewayClass); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, false, err
			}
			continue
		}
		if string(gatewayClass.Spec.ControllerName) != controllerName {
			continue
		}
		hostnames, listenerAttached, err := acceptedListenerHostnames(ctx, h.Client, gateway, parent, route)
		if err != nil {
			return nil, false, err
		}
		if listenerAttached {
			attached = true
			for _, hostname := range hostnames {
				accepted[hostname] = struct{}{}
			}
		}
	}
	hostnames := make([]gatewayv1.Hostname, 0, len(accepted))
	for hostname := range accepted {
		hostnames = append(hostnames, hostname)
	}
	sort.Slice(hostnames, func(left, right int) bool { return hostnames[left] < hostnames[right] })
	return hostnames, attached, nil
}

func acceptedListenerHostnames(ctx context.Context, kube client.Client, gateway gatewayv1.Gateway, parent gatewayv1.ParentReference, route gatewayv1.HTTPRoute) ([]gatewayv1.Hostname, bool, error) {
	accepted := make(map[gatewayv1.Hostname]struct{})
	attached := false
	for _, listener := range gateway.Spec.Listeners {
		if parent.SectionName != nil && string(*parent.SectionName) != string(listener.Name) {
			continue
		}
		if parent.Port != nil && *parent.Port != listener.Port {
			continue
		}
		if listener.Protocol != gatewayv1.HTTPProtocolType && listener.Protocol != gatewayv1.HTTPSProtocolType {
			continue
		}
		listenerHostnames := acceptedRouteHostnames(listener.Hostname, route.Spec.Hostnames)
		if listener.Hostname != nil && *listener.Hostname != "" && len(listenerHostnames) == 0 {
			continue
		}
		if listener.AllowedRoutes == nil {
			if gateway.Namespace != route.Namespace {
				continue
			}
			attached = true
			for _, hostname := range listenerHostnames {
				accepted[hostname] = struct{}{}
			}
			continue
		}
		allowed := listener.AllowedRoutes
		if allowed.Kinds != nil {
			kindAllowed := false
			for _, kind := range allowed.Kinds {
				if (kind.Group == nil || string(*kind.Group) == gatewayv1.GroupVersion.Group) && (kind.Kind == "" || string(kind.Kind) == "HTTPRoute") {
					kindAllowed = true
				}
			}
			if !kindAllowed {
				continue
			}
		}
		namespaces := allowed.Namespaces
		if namespaces == nil || namespaces.From == nil || *namespaces.From == gatewayv1.NamespacesFromSame {
			if gateway.Namespace == route.Namespace {
				attached = true
				for _, hostname := range listenerHostnames {
					accepted[hostname] = struct{}{}
				}
			}
			continue
		}
		if *namespaces.From == gatewayv1.NamespacesFromAll {
			attached = true
			for _, hostname := range listenerHostnames {
				accepted[hostname] = struct{}{}
			}
			continue
		}
		if *namespaces.From == gatewayv1.NamespacesFromSelector && namespaces.Selector != nil {
			selector, err := metav1.LabelSelectorAsSelector(namespaces.Selector)
			if err != nil {
				continue
			}
			var namespace corev1.Namespace
			if err := kube.Get(ctx, types.NamespacedName{Name: route.Namespace}, &namespace); err != nil {
				if !apierrors.IsNotFound(err) {
					return nil, false, err
				}
				continue
			}
			if selector.Matches(labels.Set(namespace.Labels)) {
				attached = true
				for _, hostname := range listenerHostnames {
					accepted[hostname] = struct{}{}
				}
			}
		}
	}
	hostnames := make([]gatewayv1.Hostname, 0, len(accepted))
	for hostname := range accepted {
		hostnames = append(hostnames, hostname)
	}
	return hostnames, attached, nil
}

func referenceGrantPermitsService(ctx context.Context, kube client.Client, route gatewayv1.HTTPRoute, serviceNamespace, serviceName string) (bool, error) {
	var grants gatewayv1.ReferenceGrantList
	if err := kube.List(ctx, &grants, client.InNamespace(serviceNamespace)); err != nil {
		return false, err
	}
	for _, grant := range grants.Items {
		fromMatches := false
		for _, from := range grant.Spec.From {
			if string(from.Group) == gatewayv1.GroupVersion.Group && string(from.Kind) == "HTTPRoute" && string(from.Namespace) == route.Namespace {
				fromMatches = true
				break
			}
		}
		if !fromMatches {
			continue
		}
		for _, to := range grant.Spec.To {
			if string(to.Group) != "" || string(to.Kind) != "Service" {
				continue
			}
			if to.Name == nil || string(*to.Name) == serviceName {
				return true, nil
			}
		}
	}
	return false, nil
}

func (h *HTTPRouteReconciler) allowedCrossNamespaceBackends(ctx context.Context, route gatewayv1.HTTPRoute) (map[types.NamespacedName]bool, error) {
	allowed := make(map[types.NamespacedName]bool)
	for _, rule := range route.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			if !isServiceBackend(backend) {
				continue
			}
			namespace := route.Namespace
			if backend.Namespace != nil {
				namespace = string(*backend.Namespace)
			}
			if namespace == route.Namespace {
				continue
			}
			key := types.NamespacedName{Namespace: namespace, Name: string(backend.Name)}
			if _, checked := allowed[key]; checked {
				continue
			}
			permitted, err := referenceGrantPermitsService(ctx, h.Client, route, namespace, key.Name)
			if err != nil {
				return nil, err
			}
			allowed[key] = permitted
		}
	}
	return allowed, nil
}

func (h *HTTPRouteReconciler) observeBackendServices(
	ctx context.Context,
	route gatewayv1.HTTPRoute,
	allowedCrossNamespace map[types.NamespacedName]bool,
) (map[types.NamespacedName]corev1.Service, []error, error) {
	services := make(map[types.NamespacedName]corev1.Service)
	var semanticErrors []error
	for _, rule := range route.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			if !isServiceBackend(backend) {
				continue
			}
			namespace := route.Namespace
			if backend.Namespace != nil {
				namespace = string(*backend.Namespace)
			}
			key := types.NamespacedName{Namespace: namespace, Name: string(backend.Name)}
			if namespace != route.Namespace && !allowedCrossNamespace[key] {
				continue
			}
			if _, observed := services[key]; observed {
				continue
			}
			var service corev1.Service
			if err := h.Get(ctx, key, &service); err != nil {
				if apierrors.IsNotFound(err) {
					semanticErrors = append(semanticErrors, err)
					continue
				}
				return nil, nil, err
			}
			services[key] = service
		}
	}
	return services, semanticErrors, nil
}

func acceptedRouteHostnames(listener *gatewayv1.Hostname, routeHostnames []gatewayv1.Hostname) []gatewayv1.Hostname {
	if listener == nil || *listener == "" {
		return append([]gatewayv1.Hostname(nil), routeHostnames...)
	}
	if len(routeHostnames) == 0 {
		return []gatewayv1.Hostname{*listener}
	}
	accepted := make([]gatewayv1.Hostname, 0, len(routeHostnames))
	for _, hostname := range routeHostnames {
		if effective, ok := effectiveHostnameIntersection(string(*listener), string(hostname)); ok {
			accepted = append(accepted, gatewayv1.Hostname(effective))
		}
	}
	return accepted
}

func effectiveHostnameIntersection(a, b string) (string, bool) {
	a, b = strings.ToLower(strings.TrimSuffix(a, ".")), strings.ToLower(strings.TrimSuffix(b, "."))
	if a == b {
		return a, true
	}
	aWildcard := strings.HasPrefix(a, "*.")
	bWildcard := strings.HasPrefix(b, "*.")
	if aWildcard && bWildcard {
		aSuffix := strings.TrimPrefix(a, "*")
		bSuffix := strings.TrimPrefix(b, "*")
		if strings.HasSuffix(aSuffix, bSuffix) {
			return a, true
		}
		if strings.HasSuffix(bSuffix, aSuffix) {
			return b, true
		}
		return "", false
	}
	if aWildcard && wildcardHostnameMatches(a, b) {
		return b, true
	}
	if bWildcard && wildcardHostnameMatches(b, a) {
		return a, true
	}
	return "", false
}

func wildcardHostnameMatches(pattern, hostname string) bool {
	suffix := strings.TrimPrefix(pattern, "*")
	return len(hostname) > len(suffix) && strings.HasSuffix(hostname, suffix)
}

func (h *HTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.HTTPRoute{}).
		Owns(&api.MikroTikDNSRecord{}).
		Owns(&api.MikroTikPortForward{}).
		Owns(&api.MikroTikRoute{}).
		Watches(&gatewayv1.ReferenceGrant{}, handler.EnqueueRequestsFromMapFunc(h.httpRoutesForReferenceGrant)).
		Watches(&gatewayv1.Gateway{}, handler.EnqueueRequestsFromMapFunc(h.httpRoutesForGateway)).
		Watches(&gatewayv1.GatewayClass{}, handler.EnqueueRequestsFromMapFunc(h.httpRoutesForGatewayClass)).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(h.httpRoutesForNamespace)).
		Complete(h)
}

func (h *HTTPRouteReconciler) httpRoutesForReferenceGrant(ctx context.Context, object client.Object) []reconcile.Request {
	grant, ok := object.(*gatewayv1.ReferenceGrant)
	if !ok {
		return nil
	}
	namespaces := make(map[string]struct{})
	for _, from := range grant.Spec.From {
		if string(from.Group) == gatewayv1.GroupVersion.Group && string(from.Kind) == "HTTPRoute" {
			namespaces[string(from.Namespace)] = struct{}{}
		}
	}
	requests := make([]reconcile.Request, 0)
	for namespace := range namespaces {
		var routes gatewayv1.HTTPRouteList
		if err := h.List(ctx, &routes, client.InNamespace(namespace)); err != nil {
			continue
		}
		for _, route := range routes.Items {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: route.Namespace, Name: route.Name}})
		}
	}
	return requests
}

func (h *HTTPRouteReconciler) httpRoutesForGateway(ctx context.Context, object client.Object) []reconcile.Request {
	gateway, ok := object.(*gatewayv1.Gateway)
	if !ok {
		return nil
	}
	var routes gatewayv1.HTTPRouteList
	if err := h.List(ctx, &routes); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for _, route := range routes.Items {
		for _, parent := range route.Spec.ParentRefs {
			if parent.Group != nil && string(*parent.Group) != gatewayv1.GroupVersion.Group {
				continue
			}
			if parent.Kind != nil && string(*parent.Kind) != "Gateway" {
				continue
			}
			namespace := route.Namespace
			if parent.Namespace != nil {
				namespace = string(*parent.Namespace)
			}
			if namespace == gateway.Namespace && string(parent.Name) == gateway.Name {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: route.Namespace, Name: route.Name}})
				break
			}
		}
	}
	return requests
}

func (h *HTTPRouteReconciler) httpRoutesForGatewayClass(ctx context.Context, object client.Object) []reconcile.Request {
	gatewayClass, ok := object.(*gatewayv1.GatewayClass)
	if !ok {
		return nil
	}
	var gateways gatewayv1.GatewayList
	if err := h.List(ctx, &gateways); err != nil {
		return nil
	}
	gatewayKeys := make(map[types.NamespacedName]struct{})
	for _, gateway := range gateways.Items {
		if string(gateway.Spec.GatewayClassName) != gatewayClass.Name {
			continue
		}
		gatewayKeys[types.NamespacedName{Namespace: gateway.Namespace, Name: gateway.Name}] = struct{}{}
	}
	if len(gatewayKeys) == 0 {
		return nil
	}
	var routes gatewayv1.HTTPRouteList
	if err := h.List(ctx, &routes); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for _, route := range routes.Items {
		for _, parent := range route.Spec.ParentRefs {
			if parent.Group != nil && string(*parent.Group) != gatewayv1.GroupVersion.Group {
				continue
			}
			if parent.Kind != nil && string(*parent.Kind) != "Gateway" {
				continue
			}
			namespace := route.Namespace
			if parent.Namespace != nil {
				namespace = string(*parent.Namespace)
			}
			if _, exists := gatewayKeys[types.NamespacedName{Namespace: namespace, Name: string(parent.Name)}]; exists {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: route.Namespace, Name: route.Name}})
				break
			}
		}
	}
	return requests
}

func (h *HTTPRouteReconciler) httpRoutesForNamespace(ctx context.Context, object client.Object) []reconcile.Request {
	if _, ok := object.(*corev1.Namespace); !ok {
		return nil
	}
	var routes gatewayv1.HTTPRouteList
	if err := h.List(ctx, &routes, client.InNamespace(object.GetName())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(routes.Items))
	for _, route := range routes.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: route.Namespace, Name: route.Name}})
	}
	return requests
}

func appendUniqueService(services []corev1.Service, service corev1.Service) []corev1.Service {
	for _, existing := range services {
		if existing.Namespace == service.Namespace && existing.Name == service.Name {
			return services
		}
	}
	return append(services, service)
}

type generatedDNSCandidate struct {
	childName string
	hostname  string
	service   types.NamespacedName
	address   string
}

func validateGeneratedDNSCandidates(source string, candidates []generatedDNSCandidate) error {
	type dnsTarget struct {
		service types.NamespacedName
		address string
	}
	targets := make(map[string]dnsTarget)
	for _, candidate := range candidates {
		hostname := strings.ToLower(strings.TrimSuffix(candidate.hostname, "."))
		if hostname == "" || candidate.address == "" || candidate.address == corev1.ClusterIPNone {
			continue
		}
		target := dnsTarget{service: candidate.service, address: candidate.address}
		if previous, exists := targets[hostname]; exists && previous != target {
			return fmt.Errorf(
				"%w: %s hostname %s targets both Service %s (%s) and Service %s (%s)",
				errGeneratedChildAmbiguity,
				source,
				hostname,
				previous.service.String(),
				previous.address,
				candidate.service.String(),
				candidate.address,
			)
		}
		targets[hostname] = target
	}
	return nil
}

func acquireGeneratedChildClaims(
	ctx context.Context,
	kube client.Client,
	owner client.Object,
	routerRef string,
	publicIP string,
	dnsCandidates []generatedDNSCandidate,
	portForwardCandidates []portForwardCandidate,
) (string, func(), error) {
	if len(dnsCandidates) == 0 && len(portForwardCandidates) == 0 {
		return routerRef, func() {}, nil
	}
	resolvedRouter, err := resolveRouterReference(ctx, kube, owner.GetNamespace(), routerRef)
	if err != nil {
		return "", nil, err
	}
	release, err := generatedClaimFences.acquire(ctx, resolvedRouter)
	if err != nil {
		return "", nil, err
	}
	verifiedRouter, err := preflightGeneratedChildClaims(
		ctx,
		kube,
		owner,
		routerRefStorage(owner.GetNamespace(), resolvedRouter),
		publicIP,
		dnsCandidates,
		portForwardCandidates,
	)
	if err != nil {
		release()
		return "", nil, err
	}
	return verifiedRouter, release, nil
}

func preflightGeneratedChildClaims(
	ctx context.Context,
	kube client.Client,
	owner client.Object,
	routerRef string,
	publicIP string,
	dnsCandidates []generatedDNSCandidate,
	portForwardCandidates []portForwardCandidate,
) (string, error) {
	if len(dnsCandidates) == 0 && len(portForwardCandidates) == 0 {
		return routerRef, nil
	}
	resolvedRouter, err := resolveRouterReference(ctx, kube, owner.GetNamespace(), routerRef)
	if err != nil {
		return "", err
	}
	currentActor := generatedClaimActorForCurrent(owner)
	for _, candidate := range dnsCandidates {
		if candidate.childName == "" {
			continue
		}
		var record api.MikroTikDNSRecord
		err := kube.Get(ctx, types.NamespacedName{Namespace: owner.GetNamespace(), Name: candidate.childName}, &record)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !metav1.IsControlledBy(&record, owner) {
			return "", generatedOwnershipConflict(owner, &record, "DNS record "+record.Namespace+"/"+record.Name, false)
		}
	}
	for _, candidate := range portForwardCandidates {
		if candidate.name == "" {
			continue
		}
		var forward api.MikroTikPortForward
		err := kube.Get(ctx, types.NamespacedName{Namespace: owner.GetNamespace(), Name: candidate.name}, &forward)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !metav1.IsControlledBy(&forward, owner) {
			return "", generatedOwnershipConflict(owner, &forward, "port forward "+forward.Namespace+"/"+forward.Name, false)
		}
	}

	desiredRouterKey := canonicalRouterClaimKey(resolvedRouter)
	var records api.MikroTikDNSRecordList
	if err := kube.List(ctx, &records); err != nil {
		return "", err
	}
	ownerHasDNSClaim := false
	for index := range records.Items {
		record := &records.Items[index]
		if !isCurrentClaimObject(record, owner) {
			continue
		}
		refs, err := generatedClaimRouterRefs(ctx, kube, record.Namespace, record, record.Status.RouterRef, record.Spec.RouterRef)
		if err != nil {
			return "", err
		}
		if !slices.Contains(refs, desiredRouterKey) {
			continue
		}
		for _, candidate := range dnsCandidates {
			if normalizeGeneratedHostname(record.Spec.Name) == normalizeGeneratedHostname(candidate.hostname) {
				ownerHasDNSClaim = true
				break
			}
		}
	}
	for index := range records.Items {
		record := &records.Items[index]
		if isCurrentClaimObject(record, owner) {
			continue
		}
		if generatedClaimHasYieldedTo(record, currentActor) {
			continue
		}
		refs, err := generatedClaimRouterRefs(ctx, kube, record.Namespace, record, record.Status.RouterRef, record.Spec.RouterRef)
		if err != nil {
			return "", err
		}
		if !slices.Contains(refs, desiredRouterKey) {
			continue
		}
		hostname := normalizeGeneratedHostname(record.Spec.Name)
		for _, candidate := range dnsCandidates {
			if hostname != normalizeGeneratedHostname(candidate.hostname) {
				continue
			}
			return "", generatedOwnershipConflict(owner, record, fmt.Sprintf("DNS hostname %s on Router %s", hostname, desiredRouterKey), ownerHasDNSClaim)
		}
	}

	var forwards api.MikroTikPortForwardList
	if err := kube.List(ctx, &forwards); err != nil {
		return "", err
	}
	desiredPublicIP := normalizePublicIP(publicIP)
	ownerHasPortForwardClaim := false
	for index := range forwards.Items {
		forward := &forwards.Items[index]
		if !isCurrentClaimObject(forward, owner) {
			continue
		}
		refs, err := generatedClaimRouterRefs(ctx, kube, forward.Namespace, forward, forward.Status.RouterRef, forward.Spec.RouterRef)
		if err != nil {
			return "", err
		}
		if !slices.Contains(refs, desiredRouterKey) || normalizePublicIP(portForwardDestinationAddress(*forward)) != desiredPublicIP {
			continue
		}
		for _, candidate := range portForwardCandidates {
			if strings.EqualFold(forward.Spec.Protocol, candidate.protocol) && forward.Spec.ExternalPort == candidate.externalPort {
				ownerHasPortForwardClaim = true
				break
			}
		}
	}
	for index := range forwards.Items {
		forward := &forwards.Items[index]
		if isCurrentClaimObject(forward, owner) {
			continue
		}
		if generatedClaimHasYieldedTo(forward, currentActor) {
			continue
		}
		refs, err := generatedClaimRouterRefs(ctx, kube, forward.Namespace, forward, forward.Status.RouterRef, forward.Spec.RouterRef)
		if err != nil {
			return "", err
		}
		if !slices.Contains(refs, desiredRouterKey) || normalizePublicIP(portForwardDestinationAddress(*forward)) != desiredPublicIP {
			continue
		}
		for _, candidate := range portForwardCandidates {
			if strings.ToLower(forward.Spec.Protocol) != candidate.protocol || forward.Spec.ExternalPort != candidate.externalPort {
				continue
			}
			return "", generatedOwnershipConflict(owner, forward, fmt.Sprintf("port %s:%d/%s on Router %s", desiredPublicIP, candidate.externalPort, candidate.protocol, desiredRouterKey), ownerHasPortForwardClaim)
		}
	}
	return routerRefStorage(owner.GetNamespace(), resolvedRouter), nil
}

func generatedClaimRouterRefs(ctx context.Context, kube client.Client, namespace string, object client.Object, additional ...string) ([]string, error) {
	refs := durableRouterTargets(object, additional...)
	if len(refs) == 0 {
		resolved, err := resolveRouterReference(ctx, kube, namespace, "")
		if err != nil {
			if errors.Is(err, errImplicitRouterSelection) {
				return nil, nil
			}
			return nil, err
		}
		return []string{canonicalRouterClaimKey(resolved)}, nil
	}
	keys := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		resolved, err := resolveRouterReference(ctx, kube, namespace, ref)
		key := routerKeyFromRef(namespace, ref)
		if err == nil {
			key = resolved
		} else if !apierrors.IsNotFound(err) && !errors.Is(err, errImplicitRouterSelection) {
			return nil, err
		}
		canonical := canonicalRouterClaimKey(key)
		if canonical == "" {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		keys = append(keys, canonical)
	}
	return keys, nil
}

type generatedClaimActor struct {
	key    string
	direct bool
}

func isCurrentClaimObject(candidate, owner client.Object) bool {
	if reflect.TypeOf(candidate) == reflect.TypeOf(owner) &&
		candidate.GetNamespace() == owner.GetNamespace() &&
		candidate.GetName() == owner.GetName() {
		return true
	}
	return metav1.IsControlledBy(candidate, owner)
}

func generatedClaimActorForCurrent(object client.Object) generatedClaimActor {
	if controller := metav1.GetControllerOf(object); controller != nil {
		return generatedClaimActor{
			key: object.GetNamespace() + "/" + controller.Kind + "/" + controller.Name + "/" + string(controller.UID),
		}
	}
	kind, direct := generatedClaimObjectKind(object)
	return generatedClaimActor{
		key:    object.GetNamespace() + "/" + kind + "/" + object.GetName() + "/" + string(object.GetUID()),
		direct: direct,
	}
}

func generatedClaimActorForExisting(object client.Object) generatedClaimActor {
	if controller := metav1.GetControllerOf(object); controller != nil {
		return generatedClaimActor{
			key: object.GetNamespace() + "/" + controller.Kind + "/" + controller.Name + "/" + string(controller.UID),
		}
	}
	kind, _ := generatedClaimObjectKind(object)
	return generatedClaimActor{
		key:    object.GetNamespace() + "/" + kind + "/" + object.GetName() + "/" + string(object.GetUID()),
		direct: true,
	}
}

func generatedClaimHasYieldedTo(object client.Object, winner generatedClaimActor) bool {
	var applied bool
	var conditions []metav1.Condition
	switch claim := object.(type) {
	case *api.MikroTikDNSRecord:
		applied = claim.Status.Applied
		conditions = claim.Status.Conditions
	case *api.MikroTikPortForward:
		applied = claim.Status.Applied
		conditions = claim.Status.Conditions
	default:
		return false
	}
	if applied {
		return false
	}
	for _, condition := range conditions {
		if condition.Status == metav1.ConditionFalse &&
			strings.Contains(condition.Message, errGeneratedChildCollision.Error()) &&
			strings.Contains(condition.Message, winner.key) {
			return true
		}
	}
	return false
}

func generatedClaimObjectKind(object client.Object) (string, bool) {
	switch object.(type) {
	case *api.MikroTikDNSRecord:
		return "MikroTikDNSRecord", true
	case *api.MikroTikPortForward:
		return "MikroTikPortForward", true
	case *corev1.Service:
		return "Service", false
	case *networkingv1.Ingress:
		return "Ingress", false
	case *gatewayv1.HTTPRoute:
		return "HTTPRoute", false
	default:
		return fmt.Sprintf("%T", object), false
	}
}

func generatedOwnershipConflict(owner client.Object, existing client.Object, claim string, ownerHasClaim bool) error {
	currentActor := generatedClaimActorForCurrent(owner)
	existingActor := generatedClaimActorForExisting(existing)
	if currentActor.direct != existingActor.direct {
		if currentActor.direct {
			return fmt.Errorf("%w: %s is waiting for generated owner %s to yield to the direct resource", errGeneratedClaimWaiting, claim, existingActor.key)
		}
		return fmt.Errorf("%w: %s is reserved by direct resource %s", errGeneratedChildCollision, claim, existingActor.key)
	}
	if currentActor.key < existingActor.key && ownerHasClaim {
		return fmt.Errorf("%w: %s is still claimed by lower-priority owner %s", errGeneratedClaimWaiting, claim, existingActor.key)
	}
	return fmt.Errorf("%w: %s remains owned by incumbent owner %s", errGeneratedChildCollision, claim, existingActor.key)
}

func normalizeGeneratedHostname(hostname string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
}

func normalizePublicIP(value string) string {
	if ip := net.ParseIP(strings.TrimSpace(value)); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(value)
}

func portForwardDestinationAddress(forward api.MikroTikPortForward) string {
	if address := strings.TrimSpace(forward.Spec.DestinationAddress); address != "" {
		return address
	}
	return strings.TrimSpace(forward.Annotations[api.PublicIPAnnotation])
}

func namespacedNameFromAPI(reference *api.NamespacedName) types.NamespacedName {
	if reference == nil {
		return types.NamespacedName{}
	}
	return types.NamespacedName{Namespace: reference.Namespace, Name: reference.Name}
}

func cleanupAmbiguousGeneratedChildren(
	ctx context.Context,
	kube client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	dnsSourceKind string,
	dnsSourceName string,
	portForwardSource string,
	cause error,
) error {
	cleanupErr := cleanupOwnedChildren(ctx, kube, scheme, owner, dnsSourceKind, dnsSourceName, portForwardSource)
	return errors.Join(cause, cleanupErr)
}

func appendUniqueServicePort(ports []corev1.ServicePort, port corev1.ServicePort) []corev1.ServicePort {
	for _, existing := range ports {
		if existing.Name == port.Name && existing.Port == port.Port && existing.Protocol == port.Protocol {
			return ports
		}
	}
	return append(ports, port)
}

func findServicePort(service corev1.Service, port int32) (corev1.ServicePort, bool) {
	for _, candidate := range service.Spec.Ports {
		if candidate.Port == port {
			return candidate, true
		}
	}
	return corev1.ServicePort{}, false
}

func preparePortForwardReconcileRequest(ctx context.Context, request portForwardReconcileRequest) (portForwardReconcileRequest, error) {
	if request.prepared {
		return request, nil
	}
	request.prepared = true
	if request.publicIP == "" {
		return request, nil
	}
	publicIP := net.ParseIP(request.publicIP)
	if publicIP == nil {
		return request, fmt.Errorf("%s annotation value %q is not an IP address", api.PublicIPAnnotation, request.publicIP)
	}
	if request.routerRef == "" {
		resolved, err := resolveRouterReference(ctx, request.kube, request.namespace, "")
		if err != nil {
			return request, err
		}
		request.routerRef = routerRefStorage(request.namespace, resolved)
	}
	type publicMatchTarget struct {
		service       types.NamespacedName
		targetAddress string
		targetPort    int32
	}
	publicMatches := make(map[string]publicMatchTarget)
	seenCandidates := make(map[string]struct{})
	for _, service := range request.services {
		if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
			continue
		}
		ports := service.Spec.Ports
		serviceKey := types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
		selected, selectedPorts := request.servicePorts[serviceKey]
		if request.requireSelectedPorts && !selectedPorts {
			continue
		}
		if selectedPorts {
			ports = selected
		}
		// Leave NodePort targetAddress empty so PortForwardReconciler resolves
		// the live node InternalIP on each drift check. Baking the IP into spec
		// made NAT flap when List order changed and stay stale after node replacement.
		targetAddress := ""
		if service.Spec.Type == corev1.ServiceTypeNodePort {
			if _, err := serviceAddress(ctx, request.kube, service); err != nil {
				return request, err
			}
		}
		for _, port := range ports {
			protocol := strings.ToLower(string(port.Protocol))
			if protocol == "" {
				protocol = "tcp"
			}
			if protocol != "tcp" && protocol != "udp" {
				continue
			}
			targetPort := port.Port
			if service.Spec.Type == corev1.ServiceTypeNodePort {
				targetPort = port.NodePort
			}
			if targetPort == 0 {
				return request, fmt.Errorf("service %s/%s has no NodePort for port %d", service.Namespace, service.Name, port.Port)
			}
			matchKey := strings.Join([]string{publicIP.String(), protocol, strconv.Itoa(int(port.Port))}, "|")
			target := publicMatchTarget{service: serviceKey, targetAddress: targetAddress, targetPort: targetPort}
			if previous, exists := publicMatches[matchKey]; exists && previous != target {
				return request, fmt.Errorf(
					"%w: %s public match %s:%d/%s targets both Service %s and Service %s",
					errGeneratedChildAmbiguity,
					request.sourceName,
					publicIP.String(),
					port.Port,
					protocol,
					previous.service.String(),
					serviceKey.String(),
				)
			}
			publicMatches[matchKey] = target
			name := "pf-" + shortHash(request.namespace+"/"+request.sourceName+"/"+service.Namespace+"/"+service.Name+"/"+strconv.Itoa(int(port.Port))+"/"+protocol)
			candidateKey := strings.Join([]string{name, serviceKey.String(), protocol, strconv.Itoa(int(port.Port)), targetAddress, strconv.Itoa(int(targetPort))}, "|")
			if _, exists := seenCandidates[candidateKey]; exists {
				continue
			}
			seenCandidates[candidateKey] = struct{}{}
			request.candidates = append(request.candidates, portForwardCandidate{
				name:          name,
				service:       serviceKey,
				protocol:      protocol,
				externalPort:  port.Port,
				targetAddress: targetAddress,
				targetPort:    targetPort,
			})
		}
	}
	return request, nil
}

func reconcileServicePortForwards(ctx context.Context, request portForwardReconcileRequest) error {
	var err error
	request, err = preparePortForwardReconcileRequest(ctx, request)
	if err != nil {
		return err
	}
	labelValue := shortHash(request.owner.GetNamespace() + "/" + request.sourceName)
	var existing api.MikroTikPortForwardList
	if err := request.kube.List(ctx, &existing, client.InNamespace(request.namespace)); err != nil {
		return err
	}
	desired := map[string]bool{}
	for _, candidate := range request.candidates {
		desired[candidate.name] = true
		var forward api.MikroTikPortForward
		err := request.kube.Get(ctx, types.NamespacedName{Name: candidate.name, Namespace: request.namespace}, &forward)
		if apierrors.IsNotFound(err) {
			forward = api.MikroTikPortForward{ObjectMeta: metav1.ObjectMeta{Name: candidate.name, Namespace: request.namespace, Labels: map[string]string{"mikrotik.operator.io/port-forward-source": labelValue}, Annotations: map[string]string{api.PublicIPAnnotation: request.publicIP}}}
			if err := controllerutil.SetControllerReference(request.owner, &forward, request.scheme); err != nil {
				return err
			}
			forward.Spec = api.MikroTikPortForwardSpec{RouterRef: request.routerRef, Protocol: candidate.protocol, ExternalPort: candidate.externalPort, TargetAddress: candidate.targetAddress, TargetPort: candidate.targetPort, DestinationAddress: request.publicIP, ServiceRef: &api.NamespacedName{Namespace: candidate.service.Namespace, Name: candidate.service.Name}}
			if err := request.kube.Create(ctx, &forward); err != nil {
				// Another watch may have reconciled the same source concurrently.
				// The generated name is deterministic, so an AlreadyExists result
				// means the desired object is already being handled by that
				// reconciliation and can be picked up on the next pass.
				if !apierrors.IsAlreadyExists(err) {
					return err
				}
			}
		} else if err != nil {
			return err
		} else {
			if !metav1.IsControlledBy(&forward, request.owner) {
				return fmt.Errorf("port forward %s/%s already exists and is not owned by %s %s/%s", forward.Namespace, forward.Name, request.owner.GetObjectKind().GroupVersionKind().Kind, request.owner.GetNamespace(), request.owner.GetName())
			}
			if forward.Spec.RouterRef != request.routerRef || forward.Spec.Protocol != candidate.protocol || forward.Spec.ExternalPort != candidate.externalPort || forward.Spec.TargetAddress != candidate.targetAddress || forward.Spec.TargetPort != candidate.targetPort || forward.Spec.DestinationAddress != request.publicIP || forward.Spec.ServiceRef == nil || forward.Spec.ServiceRef.Name != candidate.service.Name || forward.Spec.ServiceRef.Namespace != candidate.service.Namespace || forward.Annotations[api.PublicIPAnnotation] != request.publicIP {
				forward.Spec.RouterRef = request.routerRef
				forward.Spec.Protocol = candidate.protocol
				forward.Spec.ExternalPort = candidate.externalPort
				forward.Spec.TargetAddress = candidate.targetAddress
				forward.Spec.TargetPort = candidate.targetPort
				forward.Spec.DestinationAddress = request.publicIP
				forward.Spec.ServiceRef = &api.NamespacedName{Namespace: candidate.service.Namespace, Name: candidate.service.Name}
				if forward.Annotations == nil {
					forward.Annotations = map[string]string{}
				}
				forward.Annotations[api.PublicIPAnnotation] = request.publicIP
				if err := request.kube.Update(ctx, &forward); err != nil {
					return err
				}
			}
		}
	}
	for _, forward := range existing.Items {
		if !desired[forward.Name] && metav1.IsControlledBy(&forward, request.owner) {
			if err := request.kube.Delete(ctx, &forward); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (s *ServiceDNSReconciler) resolveRouterRef(ctx context.Context, service corev1.Service) (types.NamespacedName, error) {
	key, err := resolveRouterReference(ctx, s.Client, service.Namespace, service.Annotations[api.RouterRefAnnotation])
	if err == nil {
		return key, nil
	}
	if errors.Is(err, errImplicitRouterSelection) {
		return types.NamespacedName{}, fmt.Errorf(
			"service %s/%s has %s: %w; set %s",
			service.Namespace,
			service.Name,
			api.DNSNameAnnotation,
			err,
			api.RouterRefAnnotation,
		)
	}
	return types.NamespacedName{}, err
}

func (s *ServiceDNSReconciler) reconcileServiceClusterRoutes(ctx context.Context, service *corev1.Service, routerRef string) error {
	services := make([]corev1.Service, 0, 1)
	if serviceWantsClusterRoute(*service) {
		services = append(services, *service)
	}
	return reconcileOwnedClusterRoutes(ctx, s.ownedClusterRouteRequest(service, routerRef, services))
}

func (s *ServiceDNSReconciler) deleteOwnedServiceClusterRoutes(ctx context.Context, service *corev1.Service) error {
	return reconcileOwnedClusterRoutes(ctx, s.ownedClusterRouteRequest(service, "", nil))
}

func (s *ServiceDNSReconciler) ownedClusterRouteRequest(service *corev1.Service, routerRef string, services []corev1.Service) clusterRouteReconcileRequest {
	scheme := s.RuntimeScheme
	if scheme == nil {
		scheme = s.Scheme()
	}
	return clusterRouteReconcileRequest{
		kube:       s.Client,
		scheme:     scheme,
		owner:      service,
		sourceName: "service/" + service.Name,
		namespace:  service.Namespace,
		routerRef:  routerRef,
		services:   services,
	}
}

func serviceWantsClusterRoute(service corev1.Service) bool {
	if service.Annotations[api.DNSNameAnnotation] == "" {
		return false
	}
	if !isClusterIPService(service) {
		return false
	}
	return service.Spec.ClusterIP != "" && service.Spec.ClusterIP != corev1.ClusterIPNone
}

func routeGateways(ctx context.Context, kube client.Client, service corev1.Service) ([]string, error) {
	if service.Annotations[api.RouteModeAnnotation] != "" && service.Annotations[api.RouteModeAnnotation] != "all-nodes" && service.Annotations[api.RouteModeAnnotation] != "single-node" {
		return nil, fmt.Errorf("unsupported %s value %q; use all-nodes or single-node", api.RouteModeAnnotation, service.Annotations[api.RouteModeAnnotation])
	}
	var nodes corev1.NodeList
	if err := kube.List(ctx, &nodes); err != nil {
		return nil, err
	}
	unique := nodeInternalIPs(nodes.Items)
	if len(unique) == 0 {
		return nil, fmt.Errorf("no node InternalIP found for service %s/%s", service.Namespace, service.Name)
	}
	if service.Annotations[api.RouteModeAnnotation] == "single-node" {
		return unique[:1], nil
	}
	return unique, nil
}

type RouteReconciler struct {
	client.Client
	Factory ros.Factory
}

func (r *RouteReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var route api.MikroTikRoute
	if err := r.Get(ctx, req.NamespacedName, &route); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	comment := ros.ManagedComment("route", route.Name, route.Namespace)
	if !route.DeletionTimestamp.IsZero() {
		if err := cleanupRouterTargets(ctx, r.Client, r.Factory, route.Namespace, durableRouterTargets(&route, route.Status.RouterRef, route.Spec.RouterRef), "", func(ctx context.Context, client ros.Client) error {
			return client.DeleteRoute(ctx, comment)
		}); err != nil {
			return reconcile.Result{}, err
		}
		if controllerutil.ContainsFinalizer(&route, resourceFinalizer) {
			controllerutil.RemoveFinalizer(&route, resourceFinalizer)
			return reconcile.Result{}, r.Update(ctx, &route)
		}
		return reconcile.Result{}, nil
	}
	if route.Spec.Destination == "" || route.Spec.Gateway == "" {
		return r.status(ctx, &route, fmt.Errorf("destination and gateway are required"))
	}
	if !controllerutil.ContainsFinalizer(&route, resourceFinalizer) {
		controllerutil.AddFinalizer(&route, resourceFinalizer)
		if err := r.Update(ctx, &route); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	routerKey, err := resolveRouterReference(ctx, r.Client, route.Namespace, route.Spec.RouterRef)
	if err != nil {
		if errors.Is(err, errImplicitRouterSelection) {
			if route.Annotations[durableRouterTargetsAnnotation] == "" && route.Status.RouterRef != "" {
				if _, persistErr := persistDurableRouterTarget(ctx, r.Client, &route, route.Status.RouterRef); persistErr != nil {
					return reconcile.Result{}, persistErr
				}
				return reconcile.Result{}, nil
			}
			if route.Annotations[durableRouterTargetsAnnotation] != "" {
				if cleanupErr := cleanupRouterTargets(ctx, r.Client, r.Factory, route.Namespace, durableRouterTargets(&route, route.Status.RouterRef), "", func(ctx context.Context, client ros.Client) error {
					return client.DeleteRoute(ctx, comment)
				}); cleanupErr != nil {
					return r.status(ctx, &route, errors.Join(err, cleanupErr))
				}
				if _, compactErr := compactDurableRouterTarget(ctx, r.Client, &route, ""); compactErr != nil {
					return reconcile.Result{}, compactErr
				}
				return reconcile.Result{}, nil
			}
			if route.Status.RouterRef != "" {
				route.Status.RouterRef = ""
				route.Status.Applied = false
				return reconcile.Result{}, r.Status().Update(ctx, &route)
			}
		}
		return r.status(ctx, &route, err)
	}
	routerRef := routerRefStorage(route.Namespace, routerKey)
	if route.Annotations[durableRouterTargetsAnnotation] != routerRef {
		updated, err := persistDurableRouterTarget(ctx, r.Client, &route, route.Status.RouterRef, routerRef)
		if err != nil {
			return reconcile.Result{}, err
		}
		if updated {
			return reconcile.Result{}, nil
		}
	}
	if route.Annotations[durableRouterTargetsAnnotation] != routerRef {
		if err := cleanupRouterTargets(ctx, r.Client, r.Factory, route.Namespace, durableRouterTargets(&route, route.Status.RouterRef), routerRef, func(ctx context.Context, client ros.Client) error {
			return client.DeleteRoute(ctx, comment)
		}); err != nil {
			return r.status(ctx, &route, err)
		}
		if _, err := compactDurableRouterTarget(ctx, r.Client, &route, routerRef); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if route.Status.RouterRef != "" && route.Status.RouterRef != routerRef {
		route.Status.RouterRef = ""
		route.Status.Applied = false
		return reconcile.Result{}, r.Status().Update(ctx, &route)
	}
	if err := withRouterConnections(ctx, r.Client, r.Factory, routerKey, true, func(router api.MikroTikRouter, connections []routerConnection) error {
		generated := route.Labels[clusterRouteSourceLabel] != ""
		origin := route.Labels[clusterRouteOriginLabel]
		for _, connection := range connections {
			if generated && !clusterRouteAppliesToEndpoint(route.Spec.Gateway, origin, connection.Endpoint, router) {
				if err := connection.Client.DeleteRoute(ctx, comment); err != nil {
					return err
				}
				continue
			}
			if err := connection.Client.EnsureRouteWithDistance(ctx, route.Spec.Destination, route.Spec.Gateway, route.Spec.Distance, comment); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return r.status(ctx, &route, err)
	}
	oldStatus := route.Status
	route.Status.Applied = true
	route.Status.RouterRef = routerRef
	route.Status.Conditions = readyCondition(route.Status.Conditions, metav1.ConditionTrue, "Applied", "route applied")
	if reflect.DeepEqual(oldStatus, route.Status) {
		return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, r.Status().Update(ctx, &route)
}

func (r *RouteReconciler) status(ctx context.Context, route *api.MikroTikRoute, err error) (reconcile.Result, error) {
	oldStatus := route.Status
	route.Status.Applied = false
	route.Status.Conditions = readyCondition(route.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", err.Error())
	if reflect.DeepEqual(oldStatus, route.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, r.Status().Update(ctx, route)
}

func (r *RouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&api.MikroTikRoute{}).Complete(r)
}

type PortForwardReconciler struct {
	client.Client
	Factory ros.Factory
}

type FirewallRuleReconciler struct {
	client.Client
	Factory ros.Factory
}

func (r *FirewallRuleReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var rule api.MikroTikFirewallRule
	if err := r.Get(ctx, req.NamespacedName, &rule); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	comment := ros.ManagedComment("firewall", rule.Name, rule.Namespace)
	if !rule.DeletionTimestamp.IsZero() {
		if err := cleanupRouterTargets(ctx, r.Client, r.Factory, rule.Namespace, durableRouterTargets(&rule, rule.Status.RouterRef, rule.Spec.RouterRef), "", func(ctx context.Context, client ros.Client) error {
			return client.DeleteFirewallRule(ctx, comment)
		}); err != nil {
			return reconcile.Result{}, err
		}
		if controllerutil.ContainsFinalizer(&rule, resourceFinalizer) {
			controllerutil.RemoveFinalizer(&rule, resourceFinalizer)
			return reconcile.Result{}, r.Update(ctx, &rule)
		}
		return reconcile.Result{}, nil
	}
	if rule.Spec.Chain == "" || rule.Spec.Action == "" {
		return r.status(ctx, &rule, fmt.Errorf("chain and action are required"))
	}
	if !controllerutil.ContainsFinalizer(&rule, resourceFinalizer) {
		controllerutil.AddFinalizer(&rule, resourceFinalizer)
		if err := r.Update(ctx, &rule); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	routerKey, err := resolveRouterReference(ctx, r.Client, rule.Namespace, rule.Spec.RouterRef)
	if err != nil {
		if errors.Is(err, errImplicitRouterSelection) {
			if rule.Annotations[durableRouterTargetsAnnotation] == "" && rule.Status.RouterRef != "" {
				if _, persistErr := persistDurableRouterTarget(ctx, r.Client, &rule, rule.Status.RouterRef); persistErr != nil {
					return reconcile.Result{}, persistErr
				}
				return reconcile.Result{}, nil
			}
			if rule.Annotations[durableRouterTargetsAnnotation] != "" {
				if cleanupErr := cleanupRouterTargets(ctx, r.Client, r.Factory, rule.Namespace, durableRouterTargets(&rule, rule.Status.RouterRef), "", func(ctx context.Context, client ros.Client) error {
					return client.DeleteFirewallRule(ctx, comment)
				}); cleanupErr != nil {
					return r.status(ctx, &rule, errors.Join(err, cleanupErr))
				}
				if _, compactErr := compactDurableRouterTarget(ctx, r.Client, &rule, ""); compactErr != nil {
					return reconcile.Result{}, compactErr
				}
				return reconcile.Result{}, nil
			}
			if rule.Status.RouterRef != "" {
				rule.Status.RouterRef = ""
				rule.Status.Applied = false
				return reconcile.Result{}, r.Status().Update(ctx, &rule)
			}
		}
		return r.status(ctx, &rule, err)
	}
	routerRef := routerRefStorage(rule.Namespace, routerKey)
	if rule.Annotations[durableRouterTargetsAnnotation] != routerRef {
		updated, err := persistDurableRouterTarget(ctx, r.Client, &rule, rule.Status.RouterRef, routerRef)
		if err != nil {
			return reconcile.Result{}, err
		}
		if updated {
			return reconcile.Result{}, nil
		}
	}
	if rule.Annotations[durableRouterTargetsAnnotation] != routerRef {
		if err := cleanupRouterTargets(ctx, r.Client, r.Factory, rule.Namespace, durableRouterTargets(&rule, rule.Status.RouterRef), routerRef, func(ctx context.Context, client ros.Client) error {
			return client.DeleteFirewallRule(ctx, comment)
		}); err != nil {
			return r.status(ctx, &rule, err)
		}
		if _, err := compactDurableRouterTarget(ctx, r.Client, &rule, routerRef); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if rule.Status.RouterRef != "" && rule.Status.RouterRef != routerRef {
		rule.Status.RouterRef = ""
		rule.Status.Applied = false
		return reconcile.Result{}, r.Status().Update(ctx, &rule)
	}
	rosRule := ros.FirewallRule{
		Chain:              rule.Spec.Chain,
		Action:             rule.Spec.Action,
		Protocol:           rule.Spec.Protocol,
		SourceAddress:      rule.Spec.SourceAddress,
		DestinationAddress: rule.Spec.DestinationAddress,
		SourcePort:         rule.Spec.SourcePort,
		DestinationPort:    rule.Spec.DestinationPort,
		InInterface:        rule.Spec.InInterface,
		OutInterface:       rule.Spec.OutInterface,
		ConnectionState:    rule.Spec.ConnectionState,
		ConnectionNatState: rule.Spec.ConnectionNatState,
		LogPrefix:          rule.Spec.LogPrefix,
		PlaceBefore:        rule.Spec.PlaceBefore,
	}
	if err := withRouterConnections(ctx, r.Client, r.Factory, routerKey, true, func(_ api.MikroTikRouter, connections []routerConnection) error {
		for _, connection := range connections {
			if err := connection.Client.EnsureFirewallRule(ctx, rosRule, comment); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return r.status(ctx, &rule, err)
	}
	oldStatus := rule.Status
	rule.Status.Applied = true
	rule.Status.RouterRef = routerRef
	rule.Status.Conditions = readyCondition(rule.Status.Conditions, metav1.ConditionTrue, "Applied", "firewall rule applied")
	if reflect.DeepEqual(oldStatus, rule.Status) {
		return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, r.Status().Update(ctx, &rule)
}

func (r *FirewallRuleReconciler) status(ctx context.Context, rule *api.MikroTikFirewallRule, err error) (reconcile.Result, error) {
	oldStatus := rule.Status
	rule.Status.Applied = false
	rule.Status.Conditions = readyCondition(rule.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", err.Error())
	if reflect.DeepEqual(oldStatus, rule.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, r.Status().Update(ctx, rule)
}

func (r *FirewallRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&api.MikroTikFirewallRule{}).Complete(r)
}

func (p *PortForwardReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var o api.MikroTikPortForward
	if err := p.Get(ctx, req.NamespacedName, &o); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !o.DeletionTimestamp.IsZero() {
		if err := cleanupRouterTargets(ctx, p.Client, p.Factory, o.Namespace, durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef), "", func(ctx context.Context, client ros.Client) error {
			if err := client.DeletePortForward(ctx, ros.ManagedComment("portforward", o.Name, o.Namespace)); err != nil {
				return err
			}
			return client.DeleteFirewallRule(ctx, ros.ManagedComment("portforward-firewall", o.Name, o.Namespace))
		}); err != nil {
			return reconcile.Result{}, err
		}
		if controllerutil.ContainsFinalizer(&o, resourceFinalizer) {
			controllerutil.RemoveFinalizer(&o, resourceFinalizer)
			return reconcile.Result{}, p.Update(ctx, &o)
		}
		return reconcile.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(&o, resourceFinalizer) {
		controllerutil.AddFinalizer(&o, resourceFinalizer)
		if err := p.Update(ctx, &o); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	address := o.Spec.TargetAddress
	if o.Spec.ServiceRef != nil {
		var s corev1.Service
		if err := p.Get(ctx, types.NamespacedName{Name: o.Spec.ServiceRef.Name, Namespace: o.Spec.ServiceRef.Namespace}, &s); err != nil {
			if apierrors.IsNotFound(err) {
				for _, ref := range durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef) {
					if cleanupErr := p.cleanupConfiguration(ctx, &o, ref); cleanupErr != nil {
						return p.status(ctx, &o, cleanupErr)
					}
				}
			}
			return p.status(ctx, &o, err)
		}
		serviceTarget, addressErr := serviceAddress(ctx, p.Client, s)
		if addressErr != nil {
			if errors.Is(addressErr, errServiceNotAddressable) {
				if cleanupErr := p.cleanupAllConfiguration(ctx, &o); cleanupErr != nil {
					return p.status(ctx, &o, errors.Join(addressErr, cleanupErr))
				}
			}
			return p.status(ctx, &o, addressErr)
		}
		if address == "" {
			address = serviceTarget
		}
	}
	if address == "" && o.Spec.PodRef != nil {
		var pod corev1.Pod
		if err := p.Get(ctx, types.NamespacedName{Name: o.Spec.PodRef.Name, Namespace: o.Spec.PodRef.Namespace}, &pod); err != nil {
			if apierrors.IsNotFound(err) {
				for _, ref := range durableRouterTargets(&o, o.Status.RouterRef, o.Spec.RouterRef) {
					if cleanupErr := p.cleanupConfiguration(ctx, &o, ref); cleanupErr != nil {
						return p.status(ctx, &o, cleanupErr)
					}
				}
			}
			return p.status(ctx, &o, err)
		}
		address = pod.Status.PodIP
	}
	if net.ParseIP(address) == nil {
		if o.Spec.ServiceRef != nil {
			if cleanupErr := p.cleanupAllConfiguration(ctx, &o); cleanupErr != nil {
				return p.status(ctx, &o, errors.Join(fmt.Errorf("target address %q is not an IP", address), cleanupErr))
			}
		}
		return p.status(ctx, &o, fmt.Errorf("target address %q is not an IP", address))
	}
	destinationAddress := portForwardDestinationAddress(o)
	if destinationAddress != "" && net.ParseIP(destinationAddress) == nil {
		return p.status(ctx, &o, fmt.Errorf("destination address %q is not an IP", destinationAddress))
	}
	routerKey, err := resolveRouterReference(ctx, p.Client, o.Namespace, o.Spec.RouterRef)
	if err != nil {
		if !errors.Is(err, errImplicitRouterSelection) {
			return reconcile.Result{}, err
		}
		if errors.Is(err, errImplicitRouterSelection) {
			if o.Annotations[durableRouterTargetsAnnotation] == "" && o.Status.RouterRef != "" {
				if _, persistErr := persistDurableRouterTarget(ctx, p.Client, &o, o.Status.RouterRef); persistErr != nil {
					return reconcile.Result{}, persistErr
				}
				return reconcile.Result{}, nil
			}
			if o.Annotations[durableRouterTargetsAnnotation] != "" {
				if cleanupErr := p.cleanupAllConfiguration(ctx, &o); cleanupErr != nil {
					return p.status(ctx, &o, errors.Join(err, cleanupErr))
				}
				if _, compactErr := compactDurableRouterTarget(ctx, p.Client, &o, ""); compactErr != nil {
					return reconcile.Result{}, compactErr
				}
				return reconcile.Result{}, nil
			}
			if o.Status.RouterRef != "" {
				o.Status.RouterRef = ""
				o.Status.Applied = false
				return reconcile.Result{}, p.Status().Update(ctx, &o)
			}
		}
		return p.status(ctx, &o, err)
	}
	routerRef := routerRefStorage(o.Namespace, routerKey)
	if o.Annotations[durableRouterTargetsAnnotation] != routerRef {
		updated, err := persistDurableRouterTarget(ctx, p.Client, &o, o.Status.RouterRef, routerRef)
		if err != nil {
			return reconcile.Result{}, err
		}
		if updated {
			return reconcile.Result{}, nil
		}
	}
	if o.Annotations[durableRouterTargetsAnnotation] != routerRef {
		if err := cleanupRouterTargets(ctx, p.Client, p.Factory, o.Namespace, durableRouterTargets(&o, o.Status.RouterRef), routerRef, func(ctx context.Context, client ros.Client) error {
			if err := client.DeletePortForward(ctx, ros.ManagedComment("portforward", o.Name, o.Namespace)); err != nil {
				return err
			}
			return client.DeleteFirewallRule(ctx, ros.ManagedComment("portforward-firewall", o.Name, o.Namespace))
		}); err != nil {
			return p.status(ctx, &o, err)
		}
		if _, err := compactDurableRouterTarget(ctx, p.Client, &o, routerRef); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if o.Status.RouterRef != "" && o.Status.RouterRef != routerRef {
		o.Status.RouterRef = ""
		o.Status.Applied = false
		return reconcile.Result{}, p.Status().Update(ctx, &o)
	}
	_, releaseClaim, claimErr := acquireGeneratedChildClaims(
		ctx,
		p.Client,
		&o,
		routerRef,
		destinationAddress,
		nil,
		[]portForwardCandidate{{
			service:       namespacedNameFromAPI(o.Spec.ServiceRef),
			protocol:      strings.ToLower(o.Spec.Protocol),
			externalPort:  o.Spec.ExternalPort,
			targetAddress: address,
			targetPort:    o.Spec.TargetPort,
		}},
	)
	if claimErr != nil {
		if errors.Is(claimErr, errGeneratedChildCollision) {
			if cleanupErr := p.cleanupAllConfiguration(ctx, &o); cleanupErr != nil {
				return p.status(ctx, &o, errors.Join(claimErr, cleanupErr))
			}
			return p.status(ctx, &o, claimErr)
		}
		return reconcile.Result{}, claimErr
	}
	defer releaseClaim()
	comment := ros.ManagedComment("portforward", o.Name, o.Namespace)
	firewallComment := ros.ManagedComment("portforward-firewall", o.Name, o.Namespace)
	if err := withRouterConnections(ctx, p.Client, p.Factory, routerKey, true, func(_ api.MikroTikRouter, connections []routerConnection) error {
		for _, connection := range connections {
			forward := ros.PortForward{
				Protocol:     o.Spec.Protocol,
				ExternalPort: o.Spec.ExternalPort,
				Target:       address,
				TargetPort:   o.Spec.TargetPort,
				PublicIP:     destinationAddress,
			}
			if err := connection.Client.EnsurePortForward(ctx, forward, comment); err != nil {
				return err
			}
			if err := connection.Client.EnsureFirewallRule(ctx, ros.FirewallRule{
				Chain:              "forward",
				Action:             "accept",
				Protocol:           o.Spec.Protocol,
				DestinationAddress: address,
				DestinationPort:    strconv.FormatInt(int64(o.Spec.TargetPort), 10),
				PlaceBefore:        true,
			}, firewallComment); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return p.status(ctx, &o, err)
	}
	oldStatus := o.Status
	o.Status.Applied = true
	o.Status.RouterRef = routerRef
	o.Status.TargetAddress = address
	o.Status.ExternalAddress = destinationAddress
	o.Status.Conditions = readyCondition(o.Status.Conditions, metav1.ConditionTrue, "Applied", "port forward applied")
	if reflect.DeepEqual(oldStatus, o.Status) {
		return reconcile.Result{RequeueAfter: driftCheckInterval}, nil
	}
	return reconcile.Result{RequeueAfter: driftCheckInterval}, p.Status().Update(ctx, &o)
}

func (p *PortForwardReconciler) cleanupConfiguration(ctx context.Context, o *api.MikroTikPortForward, routerRef string) error {
	err := withRouterConnections(ctx, p.Client, p.Factory, routerKeyFromRef(o.Namespace, routerRef), false, func(_ api.MikroTikRouter, connections []routerConnection) error {
		for _, connection := range connections {
			if err := connection.Client.DeletePortForward(ctx, ros.ManagedComment("portforward", o.Name, o.Namespace)); err != nil {
				return err
			}
			if err := connection.Client.DeleteFirewallRule(ctx, ros.ManagedComment("portforward-firewall", o.Name, o.Namespace)); err != nil {
				return err
			}
		}
		return nil
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (p *PortForwardReconciler) cleanupAllConfiguration(ctx context.Context, o *api.MikroTikPortForward) error {
	for _, routerRef := range durableRouterTargets(o, o.Status.RouterRef, o.Spec.RouterRef) {
		if err := p.cleanupConfiguration(ctx, o, routerRef); err != nil {
			return err
		}
	}
	return nil
}
func (p *PortForwardReconciler) status(ctx context.Context, o *api.MikroTikPortForward, err error) (reconcile.Result, error) {
	oldStatus := o.Status
	o.Status.Applied = false
	o.Status.Conditions = readyCondition(o.Status.Conditions, metav1.ConditionFalse, "ApplyFailed", err.Error())
	if reflect.DeepEqual(oldStatus, o.Status) {
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}
	return reconcile.Result{RequeueAfter: time.Minute}, p.Status().Update(ctx, o)
}
func (p *PortForwardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.MikroTikPortForward{}).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(p.portForwardsForService)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(p.portForwardsForPod)).
		Complete(p)
}

func (p *PortForwardReconciler) portForwardsForService(ctx context.Context, object client.Object) []reconcile.Request {
	var forwards api.MikroTikPortForwardList
	if err := p.List(ctx, &forwards, client.InNamespace(object.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for _, forward := range forwards.Items {
		if forward.Spec.ServiceRef != nil && forward.Spec.ServiceRef.Namespace == object.GetNamespace() && forward.Spec.ServiceRef.Name == object.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: forward.Name, Namespace: forward.Namespace}})
		}
	}
	return requests
}

func (p *PortForwardReconciler) portForwardsForPod(ctx context.Context, object client.Object) []reconcile.Request {
	var forwards api.MikroTikPortForwardList
	if err := p.List(ctx, &forwards, client.InNamespace(object.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for _, forward := range forwards.Items {
		if forward.Spec.PodRef != nil && forward.Spec.PodRef.Namespace == object.GetNamespace() && forward.Spec.PodRef.Name == object.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: forward.Name, Namespace: forward.Namespace}})
		}
	}
	return requests
}

func Setup(mgr ctrl.Manager, factory ros.Factory, gatewayAPIEnabled bool, gatewayClass, gatewayController string) error {
	if err := (&RouterReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Factory: factory}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&DNSReconciler{Client: mgr.GetClient(), Factory: factory}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&ServiceDNSReconciler{Client: mgr.GetClient(), Factory: factory, RuntimeScheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&IngressReconciler{Client: mgr.GetClient(), Factory: factory, RuntimeScheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		return err
	}
	if gatewayAPIEnabled {
		if err := (&HTTPRouteReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), GatewayClass: gatewayClass, ControllerName: gatewayController}).SetupWithManager(mgr); err != nil {
			return err
		}
	}
	if err := (&RouteReconciler{Client: mgr.GetClient(), Factory: factory}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&FirewallRuleReconciler{Client: mgr.GetClient(), Factory: factory}).SetupWithManager(mgr); err != nil {
		return err
	}
	return (&PortForwardReconciler{Client: mgr.GetClient(), Factory: factory}).SetupWithManager(mgr)
}

var _ = fmt.Sprintf
