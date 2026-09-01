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

func conditionStatusOf(conditions []metav1.Condition, typeName string) metav1.ConditionStatus {
	for _, condition := range conditions {
		if condition.Type == typeName {
			return condition.Status
		}
	}
	return ""
}

func conditionReasonOf(conditions []metav1.Condition, typeName string) string {
	for _, condition := range conditions {
		if condition.Type == typeName {
			return condition.Reason
		}
	}
	return ""
}

func conditionMessageOf(conditions []metav1.Condition, typeName string) string {
	for _, condition := range conditions {
		if condition.Type == typeName {
			return condition.Message
		}
	}
	return ""
}
