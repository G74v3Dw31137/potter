package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Recreate the repository's relative layout so esbuild's source comments are
// identical. All deliberate corruption and normal generation stay in TempDir.
func bundlerFixture(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	source, err := filepath.Abs("../../internal/botguard")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	root := filepath.Join(tmp, "internal/botguard")
	if err = os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(filepath.Join(root, "upstream"), os.DirFS(filepath.Join(source, "upstream"))); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	for _, name := range []string{"sources.json", "assets/bgutils.js", "assets/linkedom.js"} {
		raw, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, name), raw, 0644); err != nil {
			t.Fatal(err)
		}
		original[name] = raw
	}
	working := filepath.Join(tmp, "tools/botguard-bundle")
	if err = os.MkdirAll(working, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(working)
	return root, original
}

func TestReadOnlyVerification(t *testing.T) {
	for _, mismatch := range []string{"", "bgutils.js", "linkedom.js", "both"} {
		t.Run("mismatch="+mismatch, func(t *testing.T) {
			root, _ := bundlerFixture(t)
			before := map[string][]byte{}
			times := map[string]os.FileInfo{}
			for _, name := range []string{"bgutils.js", "linkedom.js"} {
				path := filepath.Join(root, "assets", name)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if mismatch == name || mismatch == "both" {
					// A legal-comment-only difference must be checked, too.
					raw = append(raw, []byte("\n/*! deliberately changed legal comment */\n")...)
					if err = os.WriteFile(path, raw, 0644); err != nil {
						t.Fatal(err)
					}
				}
				before[name] = raw
				times[name], err = os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := run([]string{"-check"}, &stdout, &stderr)
			if mismatch == "" {
				if code != 0 {
					t.Fatalf("verification failed: %s", stderr.String())
				}
				if !strings.Contains(stdout.String(), "including legal comments") {
					t.Fatal(stdout.String())
				}
			} else {
				if code == 0 {
					t.Fatal("mismatch must return nonzero exit status")
				}
				for _, name := range []string{"bgutils.js", "linkedom.js"} {
					if mismatch == name || mismatch == "both" {
						if !strings.Contains(stderr.String(), "assets/"+name) {
							t.Fatalf("missing artifact: %s", stderr.String())
						}
					}
				}
			}
			for name, want := range before {
				path := filepath.Join(root, "assets", name)
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				stat, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, got) || !times[name].ModTime().Equal(stat.ModTime()) {
					t.Fatalf("verification wrote %s", name)
				}
			}
		})
	}
}

func TestNormalGenerationAndExclusiveFlags(t *testing.T) {
	root, original := bundlerFixture(t)
	for _, name := range []string{"bgutils.js", "linkedom.js"} {
		if err := os.WriteFile(filepath.Join(root, "assets", name), []byte("deliberate mismatch"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errs bytes.Buffer
	if code := run([]string{"-check", "-licenses"}, &out, &errs); code != 2 {
		t.Fatalf("conflicting modes: %d", code)
	}
	errs.Reset()
	if code := run(nil, &out, &errs); code != 0 {
		t.Fatalf("normal generation failed: %s", errs.String())
	}
	for _, name := range []string{"assets/bgutils.js", "assets/linkedom.js"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original[name], got) {
			t.Fatalf("normal generation changed expected %s", name)
		}
	}
}
