package controller

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func readyCondition(
	existing []metav1.Condition,
	status metav1.ConditionStatus,
	reason, message string,
) []metav1.Condition {
	transition := metav1.Now()
	if len(existing) == 1 && existing[0].Type == "Ready" && existing[0].Status == status && existing[0].Reason == reason && existing[0].Message == message {
		transition = existing[0].LastTransitionTime
	}
	return []metav1.Condition{{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: transition,
	}}
}
