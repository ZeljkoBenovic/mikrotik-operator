package controller

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestValidateGeneratedDNSCandidates(t *testing.T) {
	t.Parallel()

	web := types.NamespacedName{Namespace: "app", Name: "web"}
	api := types.NamespacedName{Namespace: "app", Name: "api"}
	tests := []struct {
		name       string
		candidates []generatedDNSCandidate
		wantErr    bool
	}{
		{
			name: "same hostname different services is ambiguous",
			candidates: []generatedDNSCandidate{
				{hostname: "www.example.com", service: web, address: "10.0.0.8"},
				{hostname: "www.example.com", service: api, address: "10.0.0.9"},
			},
			wantErr: true,
		},
		{
			name: "trailing-dot and case fold to the same hostname",
			candidates: []generatedDNSCandidate{
				{hostname: "WWW.Example.COM.", service: web, address: "10.0.0.8"},
				{hostname: "www.example.com", service: api, address: "10.0.0.9"},
			},
			wantErr: true,
		},
		{
			name: "same hostname and same target is idempotent",
			candidates: []generatedDNSCandidate{
				{hostname: "www.example.com", service: web, address: "10.0.0.8"},
				{hostname: "www.example.com", service: web, address: "10.0.0.8"},
			},
		},
		{
			name: "empty hostname and ClusterIPNone are skipped",
			candidates: []generatedDNSCandidate{
				{hostname: "", service: web, address: "10.0.0.8"},
				{hostname: "www.example.com", service: web, address: corev1.ClusterIPNone},
				{hostname: "www.example.com", service: api, address: ""},
				{hostname: "www.example.com", service: web, address: "10.0.0.8"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateGeneratedDNSCandidates("ingress/web", test.candidates)
			if test.wantErr {
				if !errors.Is(err, errGeneratedChildAmbiguity) {
					t.Fatalf("error = %v, want %v", err, errGeneratedChildAmbiguity)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalizeGeneratedHostname(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercases and strips trailing dot", input: "WWW.Example.COM.", want: "www.example.com"},
		{name: "trims whitespace", input: "  foo.bar  ", want: "foo.bar"},
		{name: "empty", input: "", want: ""},
		{name: "whitespace only", input: "   ", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeGeneratedHostname(test.input); got != test.want {
				t.Fatalf("normalizeGeneratedHostname(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizePublicIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trims IPv4", input: "  192.0.2.10  ", want: "192.0.2.10"},
		{name: "canonical IPv6", input: "2001:0db8:0:0:0:0:0:1", want: "2001:db8::1"},
		{name: "non-IP passes through trimmed", input: "  public.example.com  ", want: "public.example.com"},
		{name: "empty", input: "  ", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizePublicIP(test.input); got != test.want {
				t.Fatalf("normalizePublicIP(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
