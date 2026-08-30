package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *MikroTikRouter) DeepCopyInto(out *MikroTikRouter) {
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = in.Spec
	out.Spec.Routers = append([]RouterEndpoint(nil), in.Spec.Routers...)
	out.Status = in.Status
	out.Status.AppliedEndpoints = append([]RouterEndpoint(nil), in.Status.AppliedEndpoints...)
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
}
func (in *MikroTikRouter) DeepCopy() *MikroTikRouter {
	if in == nil {
		return nil
	}
	out := new(MikroTikRouter)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikRouter) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikRouterList) DeepCopyInto(out *MikroTikRouterList) {
	*out = *in
	out.ListMeta = in.ListMeta
	out.Items = make([]MikroTikRouter, len(in.Items))
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}
func (in *MikroTikRouterList) DeepCopy() *MikroTikRouterList {
	if in == nil {
		return nil
	}
	out := new(MikroTikRouterList)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikRouterList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikDNSRecord) DeepCopyInto(out *MikroTikDNSRecord) {
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = in.Spec
	if in.Spec.ServiceRef != nil {
		x := *in.Spec.ServiceRef
		out.Spec.ServiceRef = &x
	}
	out.Status = in.Status
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
}
func (in *MikroTikDNSRecord) DeepCopy() *MikroTikDNSRecord {
	if in == nil {
		return nil
	}
	out := new(MikroTikDNSRecord)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikDNSRecord) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikDNSRecordList) DeepCopyInto(out *MikroTikDNSRecordList) {
	*out = *in
	out.Items = make([]MikroTikDNSRecord, len(in.Items))
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}
func (in *MikroTikDNSRecordList) DeepCopy() *MikroTikDNSRecordList {
	if in == nil {
		return nil
	}
	out := new(MikroTikDNSRecordList)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikDNSRecordList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikPortForward) DeepCopyInto(out *MikroTikPortForward) {
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = in.Spec
	if in.Spec.ServiceRef != nil {
		x := *in.Spec.ServiceRef
		out.Spec.ServiceRef = &x
	}
	if in.Spec.PodRef != nil {
		x := *in.Spec.PodRef
		out.Spec.PodRef = &x
	}
	out.Status = in.Status
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
}
func (in *MikroTikPortForward) DeepCopy() *MikroTikPortForward {
	if in == nil {
		return nil
	}
	out := new(MikroTikPortForward)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikPortForward) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikPortForwardList) DeepCopyInto(out *MikroTikPortForwardList) {
	*out = *in
	out.Items = make([]MikroTikPortForward, len(in.Items))
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}
func (in *MikroTikPortForwardList) DeepCopy() *MikroTikPortForwardList {
	if in == nil {
		return nil
	}
	out := new(MikroTikPortForwardList)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikPortForwardList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikRoute) DeepCopyInto(out *MikroTikRoute) {
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = in.Spec
	out.Status = in.Status
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
}
func (in *MikroTikRoute) DeepCopy() *MikroTikRoute {
	if in == nil {
		return nil
	}
	out := new(MikroTikRoute)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikRoute) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikRouteList) DeepCopyInto(out *MikroTikRouteList) {
	*out = *in
	out.Items = make([]MikroTikRoute, len(in.Items))
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}
func (in *MikroTikRouteList) DeepCopy() *MikroTikRouteList {
	if in == nil {
		return nil
	}
	out := new(MikroTikRouteList)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikRouteList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikFirewallRule) DeepCopyInto(out *MikroTikFirewallRule) {
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = in.Spec
	out.Spec.ConnectionState = append([]string(nil), in.Spec.ConnectionState...)
	out.Spec.ConnectionNatState = append([]string(nil), in.Spec.ConnectionNatState...)
	out.Status = in.Status
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
}
func (in *MikroTikFirewallRule) DeepCopy() *MikroTikFirewallRule {
	if in == nil {
		return nil
	}
	out := new(MikroTikFirewallRule)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikFirewallRule) DeepCopyObject() runtime.Object { return in.DeepCopy() }
func (in *MikroTikFirewallRuleList) DeepCopyInto(out *MikroTikFirewallRuleList) {
	*out = *in
	out.Items = make([]MikroTikFirewallRule, len(in.Items))
	for i := range in.Items {
		in.Items[i].DeepCopyInto(&out.Items[i])
	}
}
func (in *MikroTikFirewallRuleList) DeepCopy() *MikroTikFirewallRuleList {
	if in == nil {
		return nil
	}
	out := new(MikroTikFirewallRuleList)
	in.DeepCopyInto(out)
	return out
}
func (in *MikroTikFirewallRuleList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
