package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "mikrotik.operator.io", Version: "v1alpha1"}

var SchemeBuilder = runtime.NewSchemeBuilder(
	addKnownTypes,
)

func AddToScheme(scheme *runtime.Scheme) error { return SchemeBuilder.AddToScheme(scheme) }

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &MikroTikRouter{}, &MikroTikRouterList{}, &MikroTikDNSRecord{}, &MikroTikDNSRecordList{}, &MikroTikPortForward{}, &MikroTikPortForwardList{}, &MikroTikRoute{}, &MikroTikRouteList{}, &MikroTikFirewallRule{}, &MikroTikFirewallRuleList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
