package uiapi

import (
	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	kindRouters       = "mikrotikrouters"
	kindDNSRecords    = "mikrotikdnsrecords"
	kindRoutes        = "mikrotikroutes"
	kindPortForwards  = "mikrotikportforwards"
	kindFirewallRules = "mikrotikfirewallrules"

	readyConditionType = "Ready"
)

// kindOrder is the stable overview/list allowlist.
var kindOrder = []string{
	kindRouters,
	kindDNSRecords,
	kindRoutes,
	kindPortForwards,
	kindFirewallRules,
}

type kindSpec struct {
	plural       string
	gvk          schema.GroupVersionKind
	newObject    func() client.Object
	newList      func() client.ObjectList
	conditionsOf func(obj client.Object) []metav1.Condition
}

func gvk(kind string) schema.GroupVersionKind {
	return api.GroupVersion.WithKind(kind)
}

var kinds = map[string]kindSpec{
	kindRouters: {
		plural:    kindRouters,
		gvk:       gvk("MikroTikRouter"),
		newObject: func() client.Object { return &api.MikroTikRouter{} },
		newList:   func() client.ObjectList { return &api.MikroTikRouterList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return obj.(*api.MikroTikRouter).Status.Conditions
		},
	},
	kindDNSRecords: {
		plural:    kindDNSRecords,
		gvk:       gvk("MikroTikDNSRecord"),
		newObject: func() client.Object { return &api.MikroTikDNSRecord{} },
		newList:   func() client.ObjectList { return &api.MikroTikDNSRecordList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return obj.(*api.MikroTikDNSRecord).Status.Conditions
		},
	},
	kindRoutes: {
		plural:    kindRoutes,
		gvk:       gvk("MikroTikRoute"),
		newObject: func() client.Object { return &api.MikroTikRoute{} },
		newList:   func() client.ObjectList { return &api.MikroTikRouteList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return obj.(*api.MikroTikRoute).Status.Conditions
		},
	},
	kindPortForwards: {
		plural:    kindPortForwards,
		gvk:       gvk("MikroTikPortForward"),
		newObject: func() client.Object { return &api.MikroTikPortForward{} },
		newList:   func() client.ObjectList { return &api.MikroTikPortForwardList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return obj.(*api.MikroTikPortForward).Status.Conditions
		},
	},
	kindFirewallRules: {
		plural:    kindFirewallRules,
		gvk:       gvk("MikroTikFirewallRule"),
		newObject: func() client.Object { return &api.MikroTikFirewallRule{} },
		newList:   func() client.ObjectList { return &api.MikroTikFirewallRuleList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return obj.(*api.MikroTikFirewallRule).Status.Conditions
		},
	},
}

func lookupKind(plural string) (kindSpec, bool) {
	spec, ok := kinds[plural]
	return spec, ok
}

func isReady(conditions []metav1.Condition) bool {
	for _, condition := range conditions {
		if condition.Type == readyConditionType {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}
