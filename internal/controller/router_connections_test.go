package controller

import (
	"context"
	"testing"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	ros "github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type closeCountingClient struct {
	recordingRouterClient
	closes int
}

func (client *closeCountingClient) Close() error {
	client.closes++
	return nil
}

func TestConnectRouterClientsClosesPartialConnections(t *testing.T) {
	t.Parallel()

	scheme := controllerTestScheme(t)
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds-a", Namespace: "app"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("secret")},
	}
	router := api.MikroTikRouter{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
		Spec: api.MikroTikRouterSpec{
			Routers: []api.RouterEndpoint{
				{Name: "a", Address: "192.0.2.10", CredentialsSecret: corev1.LocalObjectReference{Name: "creds-a"}},
				{Name: "b", Address: "192.0.2.11", CredentialsSecret: corev1.LocalObjectReference{Name: "missing"}},
			},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&secret).Build()
	first := &closeCountingClient{}
	factory := func(_ context.Context, address string, _ int32, _ bool, _, _ string) (ros.Client, error) {
		if address != "192.0.2.10" {
			t.Fatalf("unexpected dial for %s", address)
		}
		return first, nil
	}

	connections, err := connectRouterClients(context.Background(), kube, factory, router)
	if len(connections) != 0 {
		t.Fatalf("connections = %#v, want none after rollback", connections)
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("error = %v, want NotFound", err)
	}
	if first.closes != 1 {
		t.Fatalf("first client Close() count = %d, want 1", first.closes)
	}
}

func TestValidateRouterEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		router  api.MikroTikRouter
		wantErr bool
	}{
		{
			name: "legacy address and secret",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{
					Address:           "192.0.2.10",
					CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
				},
			},
		},
		{
			name: "explicit routers",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{
					Routers: []api.RouterEndpoint{{
						Name:              "a",
						Address:           "192.0.2.10",
						CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
					}},
				},
			},
		},
		{
			name: "missing legacy credentials",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec:       api.MikroTikRouterSpec{Address: "192.0.2.10"},
			},
			wantErr: true,
		},
		{
			name: "empty endpoint address",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{
					Routers: []api.RouterEndpoint{{
						Name:              "a",
						CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
					}},
				},
			},
			wantErr: true,
		},
		{
			name: "empty endpoint secret",
			router: api.MikroTikRouter{
				ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "app"},
				Spec: api.MikroTikRouterSpec{
					Routers: []api.RouterEndpoint{{Name: "a", Address: "192.0.2.10"}},
				},
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateRouterEndpoints(test.router)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
