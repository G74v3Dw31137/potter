package botguard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
)

func TestCompatibilityManifest(t *testing.T) {
	b, e := os.ReadFile("compatibility-manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	exists := func(name string) bool { _, e := os.Stat(name); return e == nil }
	if e = validateManifest(b, exists); e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"support", "duplicate", "source", "interface", "member", "metadata", "owner"} {
		t.Run(kind, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(b, &m)
			entries := m["entries"].([]any)
			entry := entries[0].(map[string]any)
			switch kind {
			case "support":
				entry["support"] = "callable"
			case "duplicate":
				m["entries"] = append(entries, entry)
			case "source":
				m["components"].(map[string]any)["goja"].(map[string]any)["source"] = "missing.go"
			case "interface":
				entry["interface"] = "bad.name"
			case "member":
				entry["members"] = []string{"bad member"}
			case "metadata":
				entry["support"] = "supported"
				delete(entry, "conversion")
			case "owner":
				entry["owner"] = "missing"
			}
			bad, _ := json.Marshal(m)
			if validateManifest(bad, exists) == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	var pins map[string]string
	pinBytes, _ := os.ReadFile("execution-pins.json")
	if e = json.Unmarshal(pinBytes, &pins); e != nil {
		t.Fatal(e)
	}
	for name, want := range pins {
		got, e := os.ReadFile(filepath.Join("../..", name))
		if e != nil || string(got) != want {
			t.Fatalf("embedded pin out of date: %s", name)
		}
	}
}
func BenchmarkFoundationInitialize(b *testing.B) {
	m := NewEngine(nil)
	defer m.Close()
	ps, e := m.staticPrograms(context.Background())
	if e != nil {
		b.Fatal(e)
	}
	b.ReportAllocs()
	for b.Loop() {
		rt := newRuntime(fixtureContext())
		if e = rt.initialize(context.Background(), fixtureContext(), ps); e != nil {
			b.Fatal(e)
		}
	}
}

// validateManifest is documentation/test validation, never property dispatch.
func validateManifest(b []byte, exists func(string) bool) error {
	var m struct {
		Version    int
		Profile    string
		Profiles   map[string]string
		Components map[string]struct{ Kind, Source string }
		Entries    []struct {
			Interface                                                                            string
			Members                                                                              []string
			Owner, Support, Placement, Conversion, Receiver, Exceptions, Discrepancies, Workload string
			Tests                                                                                []string
		}
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Version != 1 || m.Profile == "" || len(m.Profiles) != 2 || len(m.Entries) == 0 {
		return fmt.Errorf("manifest header")
	}
	kinds := map[string]bool{"engine": true, "dom-backend": true, "javascript-binding": true, "go-host": true}
	for name, c := range m.Components {
		if !kinds[c.Kind] || !exists(path.Clean(c.Source)) {
			return fmt.Errorf("invalid component %s", name)
		}
	}
	ident := func(s string) bool {
		if s == "" {
			return false
		}
		for i, c := range s {
			if !(c == '_' || c == '$' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
				return false
			}
		}
		return true
	}
	seen := map[string]bool{}
	for _, e := range m.Entries {
		if !ident(e.Interface) || len(e.Members) == 0 {
			return fmt.Errorf("invalid interface %s", e.Interface)
		}
		if _, ok := m.Components[e.Owner]; !ok {
			return fmt.Errorf("unknown owner %s", e.Owner)
		}
		switch e.Support {
		case "supported", "deterministic-profile", "known-defect", "unsupported":
		default:
			return fmt.Errorf("invalid support %s", e.Support)
		}
		if e.Placement == "" || e.Conversion == "" || e.Receiver == "" || e.Exceptions == "" || e.Workload == "" || len(e.Tests) == 0 {
			return fmt.Errorf("missing contract metadata %s", e.Interface)
		}
		if e.Support == "known-defect" && e.Discrepancies == "" {
			return fmt.Errorf("missing defect")
		}
		for _, member := range e.Members {
			k := e.Interface + "." + member
			if !ident(member) || seen[k] {
				return fmt.Errorf("invalid/duplicate declaration %s", k)
			}
			seen[k] = true
		}
	}
	return nil
}

// Keep source-origin verification independent of the removed replay identities.
func TestUpstreamSourceIntegrity(t *testing.T) {
	raw, err := os.ReadFile("sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var pins map[string]string
	if err = json.Unmarshal(raw, &pins); err != nil {
		t.Fatal(err)
	}
	for name, want := range pins {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
				t.Fatalf("source pin mismatch: %s", name)
			}
		})
	}
	for i, source := range staticSources() {
		if source.Name == "image-state" && (i == 0 || staticSources()[i-1].Name != "webidl") {
			t.Fatal("WebIDL must precede image adapter")
		}
	}
}
