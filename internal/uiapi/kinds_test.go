package uiapi

import (
	"testing"
)

func TestLookupKindAllowlist(t *testing.T) {
	t.Parallel()

	if len(kinds) != len(kindOrder) {
		t.Fatalf("kinds map has %d entries, kindOrder has %d", len(kinds), len(kindOrder))
	}
	seen := map[string]bool{}
	for _, plural := range kindOrder {
		if seen[plural] {
			t.Fatalf("duplicate kind %q in kindOrder", plural)
		}
		seen[plural] = true
		spec, ok := lookupKind(plural)
		if !ok {
			t.Fatalf("kindOrder entry %q missing from kinds", plural)
		}
		if spec.plural != plural {
			t.Fatalf("spec.plural %q want %q", spec.plural, plural)
		}
		if spec.newObject == nil || spec.newList == nil || spec.conditionsOf == nil {
			t.Fatalf("kind %q is missing constructors", plural)
		}
		obj := spec.newObject()
		if obj == nil {
			t.Fatalf("kind %q newObject returned nil", plural)
		}
		list := spec.newList()
		if list == nil {
			t.Fatalf("kind %q newList returned nil", plural)
		}
		if got := spec.conditionsOf(obj); got == nil {
			// A newly allocated object has a nil slice, which is still valid.
			continue
		}
	}
}

func TestLookupKindRejectsUnknownAndAliasedNames(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"pods",
		"secrets",
		"namespaces",
		"MikroTikRouter",
		"mikrotikrouter",
		"MIKROTIKROUTERS",
		"mikrotikrouters ",
		"mikrotik-routers",
		"configmaps",
	}
	for _, kind := range tests {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			if _, ok := lookupKind(kind); ok {
				t.Fatalf("lookupKind(%q) succeeded, want reject", kind)
			}
		})
	}
}

func TestLookupKindKnownPlurals(t *testing.T) {
	t.Parallel()

	wantGVK := map[string]string{
		kindRouters:       "MikroTikRouter",
		kindDNSRecords:    "MikroTikDNSRecord",
		kindRoutes:        "MikroTikRoute",
		kindPortForwards:  "MikroTikPortForward",
		kindFirewallRules: "MikroTikFirewallRule",
	}
	for plural, kind := range wantGVK {
		t.Run(plural, func(t *testing.T) {
			t.Parallel()
			spec, ok := lookupKind(plural)
			if !ok {
				t.Fatalf("missing %q", plural)
			}
			if spec.gvk.Kind != kind {
				t.Fatalf("gvk.Kind %q want %q", spec.gvk.Kind, kind)
			}
			if spec.gvk.Group != "mikrotik.operator.io" || spec.gvk.Version != "v1alpha1" {
				t.Fatalf("gvk %#v", spec.gvk)
			}
		})
	}
}
