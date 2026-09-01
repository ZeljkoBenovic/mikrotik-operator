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
	kindBackups       = "mikrotikbackups"
	kindRestores      = "mikrotikrestores"

	readyConditionType = "Ready"
)

// kindOrder is the stable overview/list allowlist.
var kindOrder = []string{
	kindRouters,
	kindDNSRecords,
	kindRoutes,
	kindPortForwards,
	kindFirewallRules,
	kindBackups,
	kindRestores,
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
			return objectConditions(obj, func(o *api.MikroTikRouter) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
	kindDNSRecords: {
		plural:    kindDNSRecords,
		gvk:       gvk("MikroTikDNSRecord"),
		newObject: func() client.Object { return &api.MikroTikDNSRecord{} },
		newList:   func() client.ObjectList { return &api.MikroTikDNSRecordList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return objectConditions(obj, func(o *api.MikroTikDNSRecord) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
	kindRoutes: {
		plural:    kindRoutes,
		gvk:       gvk("MikroTikRoute"),
		newObject: func() client.Object { return &api.MikroTikRoute{} },
		newList:   func() client.ObjectList { return &api.MikroTikRouteList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return objectConditions(obj, func(o *api.MikroTikRoute) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
	kindPortForwards: {
		plural:    kindPortForwards,
		gvk:       gvk("MikroTikPortForward"),
		newObject: func() client.Object { return &api.MikroTikPortForward{} },
		newList:   func() client.ObjectList { return &api.MikroTikPortForwardList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return objectConditions(obj, func(o *api.MikroTikPortForward) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
	kindFirewallRules: {
		plural:    kindFirewallRules,
		gvk:       gvk("MikroTikFirewallRule"),
		newObject: func() client.Object { return &api.MikroTikFirewallRule{} },
		newList:   func() client.ObjectList { return &api.MikroTikFirewallRuleList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return objectConditions(obj, func(o *api.MikroTikFirewallRule) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
	kindBackups: {
		plural:    kindBackups,
		gvk:       gvk("MikroTikBackup"),
		newObject: func() client.Object { return &api.MikroTikBackup{} },
		newList:   func() client.ObjectList { return &api.MikroTikBackupList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return objectConditions(obj, func(o *api.MikroTikBackup) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
	kindRestores: {
		plural:    kindRestores,
		gvk:       gvk("MikroTikRestore"),
		newObject: func() client.Object { return &api.MikroTikRestore{} },
		newList:   func() client.ObjectList { return &api.MikroTikRestoreList{} },
		conditionsOf: func(obj client.Object) []metav1.Condition {
			return objectConditions(obj, func(o *api.MikroTikRestore) []metav1.Condition {
				return o.Status.Conditions
			})
		},
	},
}

func objectConditions[T client.Object](obj client.Object, conds func(T) []metav1.Condition) []metav1.Condition {
	typed, ok := obj.(T)
	if !ok {
		return nil
	}
	return conds(typed)
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
