package uiapi

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestControllerOwner(t *testing.T) {
	t.Parallel()

	owned := true
	notOwned := false
	tests := []struct {
		name string
		obj  client.Object
		want *managedBy
	}{
		{
			name: "no owners",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "app"},
			},
		},
		{
			name: "non-controller owner is ignored",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "shared",
					Namespace: "app",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       "creds",
						Controller: &notOwned,
					}},
				},
			},
		},
		{
			name: "controller owner",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "child",
					Namespace: "app",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "v1",
						Kind:       "Service",
						Name:       "web",
						Controller: &owned,
					}},
				},
			},
			want: &managedBy{
				APIVersion: "v1",
				Kind:       "Service",
				Namespace:  "app",
				Name:       "web",
			},
		},
		{
			name: "first controller wins",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "child",
					Namespace: "app",
					OwnerReferences: []metav1.OwnerReference{
						{APIVersion: "v1", Kind: "Secret", Name: "creds"},
						{APIVersion: "v1", Kind: "Service", Name: "web", Controller: &owned},
					},
				},
			},
			want: &managedBy{
				APIVersion: "v1",
				Kind:       "Service",
				Namespace:  "app",
				Name:       "web",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := controllerOwner(tt.obj)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %#v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected owner")
			}
			if *got != *tt.want {
				t.Fatalf("got %#v want %#v", *got, *tt.want)
			}
		})
	}
}

func TestOwnedConflictMessage(t *testing.T) {
	t.Parallel()
	got := ownedConflictMessage(&managedBy{Kind: "Service", Name: "web", Namespace: "app"})
	want := "resource is owned by Service/web in namespace app"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = ownedConflictMessage(&managedBy{Kind: "Node", Name: "n1"})
	want = "resource is owned by Node/n1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
