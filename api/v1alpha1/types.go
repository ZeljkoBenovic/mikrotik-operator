package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PublicIPAnnotation   = "mikrotik.operator.io/public-ip"
	DNSNameAnnotation    = "mikrotik.operator.io/dns-name"
	RouterRefAnnotation  = "mikrotik.operator.io/router-ref"
	RouteModeAnnotation  = "mikrotik.operator.io/route-mode"
	ManagedCommentPrefix = "managed-by=mikrotik-operator"
	IngressClassName     = "mikrotik"
	IngressController    = "mikrotik.operator.io/controller"
	GatewayClassName     = "mikrotik"
	GatewayController    = "mikrotik.operator.io/controller"
)

// +kubebuilder:validation:XValidation:rule="(has(self.routers) && size(self.routers) > 0 && self.routers.all(r, size(r.address) > 0 && has(r.credentialsSecret) && size(r.credentialsSecret.name) > 0)) || (has(self.address) && size(self.address) > 0 && has(self.credentialsSecret) && size(self.credentialsSecret.name) > 0)",message="spec requires valid routers entries or a non-empty legacy address and credentialsSecret"
type MikroTikRouterSpec struct {
	// +kubebuilder:validation:MinLength=1
	Address           string                      `json:"address,omitempty"`
	Port              int32                       `json:"port,omitempty"`
	TLS               bool                        `json:"tls,omitempty"`
	CredentialsSecret corev1.LocalObjectReference `json:"credentialsSecret,omitempty"`
	RouteGateway      string                      `json:"routeGateway,omitempty"`
	Routers           []RouterEndpoint            `json:"routers,omitempty"`
}
type RouterEndpoint struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Address           string                      `json:"address"`
	Port              int32                       `json:"port,omitempty"`
	TLS               bool                        `json:"tls,omitempty"`
	CredentialsSecret corev1.LocalObjectReference `json:"credentialsSecret"`
	RouteGateway      string                      `json:"routeGateway,omitempty"`
}
type MikroTikRouterStatus struct {
	Connected        bool               `json:"connected"`
	Version          string             `json:"version,omitempty"`
	AppliedEndpoints []RouterEndpoint   `json:"appliedEndpoints,omitempty"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
}
type MikroTikRouter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikRouterSpec   `json:"spec"`
	Status            MikroTikRouterStatus `json:"status,omitempty"`
}
type MikroTikRouterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikRouter `json:"items"`
}

type MikroTikDNSRecordSpec struct {
	RouterRef  string          `json:"routerRef"`
	Name       string          `json:"name"`
	Address    string          `json:"address"`
	TTL        string          `json:"ttl,omitempty"`
	ServiceRef *NamespacedName `json:"serviceRef,omitempty"`
}
type MikroTikDNSRecordStatus struct {
	Applied    bool               `json:"applied"`
	RouterID   string             `json:"routerID,omitempty"`
	RouterRef  string             `json:"routerRef,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
type MikroTikDNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikDNSRecordSpec   `json:"spec"`
	Status            MikroTikDNSRecordStatus `json:"status,omitempty"`
}
type MikroTikDNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikDNSRecord `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.targetAddress) && !has(self.podRef)) || (!has(self.targetAddress) && has(self.serviceRef) && !has(self.podRef)) || (!has(self.targetAddress) && !has(self.serviceRef) && has(self.podRef))",message="spec must target an address, Service, or Pod; targetAddress may be combined only with serviceRef for dependency tracking"
type MikroTikPortForwardSpec struct {
	RouterRef          string          `json:"routerRef"`
	Protocol           string          `json:"protocol"`
	ExternalPort       int32           `json:"externalPort"`
	ServiceRef         *NamespacedName `json:"serviceRef,omitempty"`
	PodRef             *NamespacedName `json:"podRef,omitempty"`
	TargetPort         int32           `json:"targetPort"`
	TargetAddress      string          `json:"targetAddress,omitempty"`
	DestinationAddress string          `json:"destinationAddress,omitempty"`
}
type NamespacedName struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
type MikroTikPortForwardStatus struct {
	Applied         bool               `json:"applied"`
	RouterID        string             `json:"routerID,omitempty"`
	RouterRef       string             `json:"routerRef,omitempty"`
	TargetAddress   string             `json:"targetAddress,omitempty"`
	ExternalAddress string             `json:"externalAddress,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}
type MikroTikPortForward struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikPortForwardSpec   `json:"spec"`
	Status            MikroTikPortForwardStatus `json:"status,omitempty"`
}
type MikroTikPortForwardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikPortForward `json:"items"`
}

type MikroTikRouteSpec struct {
	RouterRef   string `json:"routerRef,omitempty"`
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Distance    int32  `json:"distance,omitempty"`
}
type MikroTikRouteStatus struct {
	Applied    bool               `json:"applied"`
	RouterRef  string             `json:"routerRef,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
type MikroTikRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikRouteSpec   `json:"spec"`
	Status            MikroTikRouteStatus `json:"status,omitempty"`
}
type MikroTikRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikRoute `json:"items"`
}

type MikroTikFirewallRuleSpec struct {
	RouterRef          string   `json:"routerRef,omitempty"`
	Chain              string   `json:"chain"`
	Action             string   `json:"action"`
	Protocol           string   `json:"protocol,omitempty"`
	SourceAddress      string   `json:"sourceAddress,omitempty"`
	DestinationAddress string   `json:"destinationAddress,omitempty"`
	SourcePort         string   `json:"sourcePort,omitempty"`
	DestinationPort    string   `json:"destinationPort,omitempty"`
	InInterface        string   `json:"inInterface,omitempty"`
	OutInterface       string   `json:"outInterface,omitempty"`
	ConnectionState    []string `json:"connectionState,omitempty"`
	ConnectionNatState []string `json:"connectionNatState,omitempty"`
	LogPrefix          string   `json:"logPrefix,omitempty"`
	PlaceBefore        bool     `json:"placeBefore,omitempty"`
}
type MikroTikFirewallRuleStatus struct {
	Applied    bool               `json:"applied"`
	RouterRef  string             `json:"routerRef,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
type MikroTikFirewallRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikFirewallRuleSpec   `json:"spec"`
	Status            MikroTikFirewallRuleStatus `json:"status,omitempty"`
}
type MikroTikFirewallRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikFirewallRule `json:"items"`
}
