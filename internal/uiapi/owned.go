package uiapi

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type managedBy struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

func controllerOwner(obj client.Object) *managedBy {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		return &managedBy{
			APIVersion: ref.APIVersion,
			Kind:       ref.Kind,
			Namespace:  obj.GetNamespace(),
			Name:       ref.Name,
		}
	}
	return nil
}

func ownedConflictMessage(owner *managedBy) string {
	if owner.Namespace == "" {
		return fmt.Sprintf("resource is owned by %s/%s", owner.Kind, owner.Name)
	}
	return fmt.Sprintf(
		"resource is owned by %s/%s in namespace %s",
		owner.Kind,
		owner.Name,
		owner.Namespace,
	)
}
