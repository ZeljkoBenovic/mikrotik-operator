package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestFindIngressServicePort(t *testing.T) {
	t.Parallel()

	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(8080), Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 443, TargetPort: intstr.FromInt(8443), Protocol: corev1.ProtocolTCP},
			},
		},
	}
	tests := []struct {
		name   string
		port   networkingv1.ServiceBackendPort
		want   string
		wantOK bool
	}{
		{name: "named port", port: networkingv1.ServiceBackendPort{Name: "https"}, want: "https", wantOK: true},
		{name: "numbered port", port: networkingv1.ServiceBackendPort{Number: 80}, want: "http", wantOK: true},
		{
			name:   "mixed name and number matches the first candidate that satisfies either",
			port:   networkingv1.ServiceBackendPort{Name: "https", Number: 80},
			want:   "http",
			wantOK: true,
		},
		{name: "unknown name falls back to number", port: networkingv1.ServiceBackendPort{Name: "metrics", Number: 80}, want: "http", wantOK: true},
		{name: "missing name and number", port: networkingv1.ServiceBackendPort{Name: "metrics", Number: 9090}},
		{name: "zero number and empty name", port: networkingv1.ServiceBackendPort{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := findIngressServicePort(service, test.port)
			if ok != test.wantOK {
				t.Fatalf("ok = %t, want %t (got %#v)", ok, test.wantOK, got)
			}
			if !test.wantOK {
				return
			}
			if got.Name != test.want {
				t.Fatalf("port name = %q, want %q", got.Name, test.want)
			}
		})
	}
}
