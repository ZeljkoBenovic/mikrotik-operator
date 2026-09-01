package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestFirewallRuleReconcilerAppliesSpecToRouterOS(t *testing.T) {
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
	rule := api.MikroTikFirewallRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web-allow",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: router.Name},
		},
		Spec: api.MikroTikFirewallRuleSpec{
			RouterRef:          router.Name,
			Chain:              "forward",
			Action:             "accept",
			Protocol:           "tcp",
			SourceAddress:      "10.0.0.0/24",
			DestinationAddress: "10.0.0.20",
			DestinationPort:    "443",
			ConnectionState:    []string{"new"},
			PlaceBefore:        true,
		},
	}
	routerClient := &recordingRouterClient{}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&router, &secret, &rule).
		WithStatusSubresource(&router, &rule).
		Build()
	reconciler := FirewallRuleReconciler{Client: kube, Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
		return routerClient, nil
	}}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(rule.Namespace, rule.Name)); err != nil {
		t.Fatal(err)
	}
	if len(routerClient.ensuredFirewallRules) != 1 {
		t.Fatalf("ensured firewall rules = %d, want 1", len(routerClient.ensuredFirewallRules))
	}
	got := routerClient.ensuredFirewallRules[0]
	if got.Chain != "forward" || got.Action != "accept" || got.Protocol != "tcp" {
		t.Fatalf("unexpected firewall rule: %#v", got)
	}
	if got.SourceAddress != "10.0.0.0/24" || got.DestinationAddress != "10.0.0.20" || got.DestinationPort != "443" {
		t.Fatalf("unexpected firewall matchers: %#v", got)
	}
	if !got.PlaceBefore {
		t.Fatal("PlaceBefore was not forwarded to RouterOS")
	}
	var stored api.MikroTikFirewallRule
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: rule.Namespace, Name: rule.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Status.Applied || stored.Status.RouterRef != router.Name {
		t.Fatalf("status = %#v, want applied on %s", stored.Status, router.Name)
	}
	if len(stored.Status.Conditions) != 1 || stored.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("ready condition = %#v", stored.Status.Conditions)
	}
}

func TestFirewallRuleReconcilerRejectsMissingChainOrAction(t *testing.T) {
	scheme := controllerTestScheme(t)
	rule := api.MikroTikFirewallRule{
		ObjectMeta: metav1.ObjectMeta{Name: "incomplete", Namespace: "app"},
		Spec:       api.MikroTikFirewallRuleSpec{Action: "drop"},
	}
	kube := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&rule).
		WithStatusSubresource(&rule).
		Build()
	dials := 0
	reconciler := FirewallRuleReconciler{
		Client: kube,
		Factory: func(context.Context, string, int32, bool, string, string) (ros.Client, error) {
			dials++
			return nil, nil
		},
	}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(rule.Namespace, rule.Name)); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("invalid firewall rule dialed RouterOS %d times", dials)
	}
	var stored api.MikroTikFirewallRule
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: rule.Namespace, Name: rule.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.Applied {
		t.Fatal("incomplete firewall rule marked applied")
	}
	if len(stored.Status.Conditions) == 0 || !strings.Contains(stored.Status.Conditions[0].Message, "chain and action") {
		t.Fatalf("status message = %#v, want chain and action error", stored.Status.Conditions)
	}
}

func TestFirewallRuleReconcilerIgnoresMissingObject(t *testing.T) {
	scheme := controllerTestScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := FirewallRuleReconciler{Client: kube}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest("app", "missing")); err != nil {
		t.Fatal(err)
	}
}

func TestFirewallRuleReconcilerDeletesRouterOSOnDeletion(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	now := metav1.NewTime(time.Now())
	rule := api.MikroTikFirewallRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "web-allow",
			Namespace:         "app",
			Finalizers:        []string{resourceFinalizer},
			DeletionTimestamp: &now,
			Annotations:       map[string]string{durableRouterTargetsAnnotation: "router-a,router-b"},
		},
		Spec:   api.MikroTikFirewallRuleSpec{Chain: "forward", Action: "drop", RouterRef: "router-b"},
		Status: api.MikroTikFirewallRuleStatus{RouterRef: "router-b", Applied: true},
	}
	objects = append(objects, &rule)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&rule).Build()
	reconciler := FirewallRuleReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(rule.Namespace, rule.Name)); err != nil {
		t.Fatal(err)
	}
	wantComment := ros.ManagedComment("firewall", rule.Name, rule.Namespace)
	for name, routerClient := range clients {
		if routerClient.deletedFirewall == 0 {
			t.Fatalf("%s was not cleaned: deletedFirewall=%d", name, routerClient.deletedFirewall)
		}
		if len(routerClient.deletedFirewallComments) == 0 || routerClient.deletedFirewallComments[0] != wantComment {
			t.Fatalf("%s deleted comments %#v, want %q", name, routerClient.deletedFirewallComments, wantComment)
		}
	}
	var stored api.MikroTikFirewallRule
	err := kube.Get(context.Background(), types.NamespacedName{Namespace: rule.Namespace, Name: rule.Name}, &stored)
	if err == nil && controllerutil.ContainsFinalizer(&stored, resourceFinalizer) {
		t.Fatal("deletion left the managed-config finalizer")
	}
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestFirewallRuleReconcilerCleansDurableTargetWhenRouterSelectionIsAmbiguous(t *testing.T) {
	scheme, objects, factory, clients := externalCleanupFixture(t)
	rule := api.MikroTikFirewallRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "web-allow",
			Namespace:   "app",
			Finalizers:  []string{resourceFinalizer},
			Annotations: map[string]string{durableRouterTargetsAnnotation: "router-a"},
		},
		Spec:   api.MikroTikFirewallRuleSpec{Chain: "input", Action: "drop"},
		Status: api.MikroTikFirewallRuleStatus{RouterRef: "router-a", Applied: true},
	}
	objects = append(objects, &rule)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&rule).Build()
	reconciler := FirewallRuleReconciler{Client: kube, Factory: factory}
	if _, err := reconciler.Reconcile(context.Background(), reconcileRequest(rule.Namespace, rule.Name)); err != nil {
		t.Fatal(err)
	}
	if clients["router-a"].deletedFirewall == 0 {
		t.Fatal("ambiguous implicit router selection did not delete the previous firewall rule")
	}
	if clients["router-b"].deletedFirewall != 0 {
		t.Fatal("ambiguous selection cleaned a router that was not in durable history")
	}
	var stored api.MikroTikFirewallRule
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: rule.Namespace, Name: rule.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Annotations[durableRouterTargetsAnnotation] != "" {
		t.Fatalf("durable router annotation = %q, want cleared", stored.Annotations[durableRouterTargetsAnnotation])
	}
}
