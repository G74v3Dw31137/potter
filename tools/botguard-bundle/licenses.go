package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	licensesBegin = "<!-- BEGIN GENERATED LICENSES -->"
	licensesEnd   = "<!-- END GENERATED LICENSES -->"
)

type component struct{ Name, Version, URL, Integrity, License string }
type licenseFile struct{ name, body string }
type packageNotice struct {
	component
	files []licenseFile
}

// Explicit maintainer-only collection; no npm executable or package code runs.
// All packages are collected in memory before the caller can write anything.
func collectLicenses(root string, client *http.Client) ([]packageNotice, error) {
	raw, err := os.ReadFile(filepath.Join(root, "upstream/linkedom-runtime-lock.json"))
	if err != nil {
		return nil, err
	}
	var packages []component
	if err = json.Unmarshal(raw, &packages); err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("empty license dependency lock")
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Name == packages[j].Name {
			return packages[i].Version < packages[j].Version
		}
		return packages[i].Name < packages[j].Name
	})
	var notices []packageNotice
	for i, p := range packages {
		if p.Name == "" || p.Version == "" || p.URL == "" || p.Integrity == "" || p.License == "" {
			return nil, fmt.Errorf("incomplete license dependency: %s %s", p.Name, p.Version)
		}
		if i > 0 && p.Name == packages[i-1].Name && p.Version == packages[i-1].Version {
			return nil, fmt.Errorf("duplicate license dependency: %s %s", p.Name, p.Version)
		}
		notice, err := collectPackageLicense(p, client)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", p.Name, p.Version, err)
		}
		notices = append(notices, notice)
	}
	return notices, nil
}

func collectPackageLicense(p component, client *http.Client) (packageNotice, error) {
	notice := packageNotice{component: p}
	resp, err := client.Get(p.URL)
	if err != nil {
		return notice, err
	}
	data, err := readLimited(resp.Body, 16<<20)
	resp.Body.Close()
	if err != nil {
		return notice, err
	}
	if resp.StatusCode != http.StatusOK {
		return notice, fmt.Errorf("license archive HTTP %d", resp.StatusCode)
	}
	sha := sha512.Sum512(data)
	if "sha512-"+base64.StdEncoding.EncodeToString(sha[:]) != p.Integrity {
		return notice, fmt.Errorf("archive integrity mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return notice, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return notice, err
		}
		// Read only top-level legal files, never extract paths to disk.
		name := strings.TrimPrefix(h.Name, "package/")
		if strings.Contains(name, "/") {
			continue
		}
		upper := strings.ToUpper(name)
		if !strings.HasPrefix(upper, "LICENSE") && !strings.HasPrefix(upper, "LICENCE") && !strings.HasPrefix(upper, "NOTICE") && upper != "COPYING" {
			continue
		}
		if (h.Typeflag != tar.TypeReg && h.Typeflag != '\x00') || seen[name] {
			return notice, fmt.Errorf("non-regular or duplicate legal file: %s", name)
		}
		seen[name] = true
		body, err := readLimited(tr, 1<<20)
		if err != nil {
			return notice, err
		}
		if strings.TrimSpace(string(body)) == "" {
			return notice, fmt.Errorf("empty legal file: %s", name)
		}
		notice.files = append(notice.files, licenseFile{name, normalizeLicense(string(body))})
	}
	if len(notice.files) == 0 {
		return notice, fmt.Errorf("no license in archive")
	}
	sort.Slice(notice.files, func(i, j int) bool { return notice.files[i].name < notice.files[j].name })
	return notice, nil
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = fmt.Errorf("license data exceeds %d bytes", limit)
	}
	return body, err
}

func normalizeLicense(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func licenseHeading(p component) string { return "## npm: " + p.Name + " " + p.Version }

// One canonical renderer for refresh and check. Preserve legal wording and
// whitespace, apart from LF line endings and a final newline before the fence.
func renderLicense(p packageNotice) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Bundled LinkeDOM component: %s %s\nSource: %s\nDeclared license: %s\n", p.Name, p.Version, p.URL, p.License)
	for _, file := range p.files {
		fmt.Fprintf(&text, "\n--- %s ---\n%s", file.name, file.body)
	}
	body := text.String()
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	// A packaged legal text may itself contain Markdown fences.
	fence := "```"
	for strings.Contains(body, fence) {
		fence += "`"
	}
	return licenseHeading(p.component) + "\n\n" + fence + "text\n" + body + fence + "\n\n"
}

func renderLicenses(notices []packageNotice) []byte {
	var text strings.Builder
	text.WriteString("\n\n")
	for _, p := range notices {
		text.WriteString(renderLicense(p))
	}
	return []byte(text.String())
}

// Offsets delimit only the marker interior, leaving both markers and all manual
// bytes untouched. A marker must occupy an entire line; CRLF is accepted outside.
func licenseBounds(doc []byte) (int, int, error) {
	start, end := -1, -1
	for offset := 0; offset < len(doc); {
		n := bytes.IndexByte(doc[offset:], '\n')
		next := len(doc)
		if n >= 0 {
			next = offset + n + 1
		}
		line := strings.TrimSuffix(strings.TrimSuffix(string(doc[offset:next]), "\n"), "\r")
		switch {
		case line == licensesBegin && start == -1 && end == -1:
			start = offset + len(licensesBegin)
		case line == licensesEnd && start >= 0 && end == -1:
			end = offset
		case strings.Contains(line, "GENERATED LICENSES"):
			return 0, 0, fmt.Errorf("malformed, duplicate or out-of-order generated license markers")
		}
		offset = next
	}
	if start < 0 || end < 0 {
		return 0, 0, fmt.Errorf("missing generated license markers")
	}
	return start, end, nil
}

func updateLicenses(root string, check bool) error {
	path := filepath.Join(root, "../../THIRD_PARTY.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	start, end, err := licenseBounds(doc)
	if err != nil {
		return err
	}
	notices, err := collectLicenses(root, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	expected := renderLicenses(notices)
	if bytes.Equal(doc[start:end], expected) {
		return nil
	}
	if check {
		var changed []string
		for _, p := range notices {
			if !bytes.Contains(doc[start:end], []byte(renderLicense(p))) {
				changed = append(changed, p.Name+" "+p.Version)
			}
		}
		if len(changed) == 0 {
			return fmt.Errorf("generated licenses differ: unexpected sections, ordering or formatting")
		}
		return fmt.Errorf("generated licenses differ: %s", strings.Join(changed, ", "))
	}
	updated := make([]byte, 0, start+len(expected)+len(doc)-end)
	updated = append(updated, doc[:start]...)
	updated = append(updated, expected...)
	updated = append(updated, doc[end:]...)
	return replaceLicenseDocument(path, doc, updated)
}

// Stage in the same directory, close successfully, then rename atomically.
// Check never calls this function, even when the document is stale.
func replaceLicenseDocument(path string, original, updated []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("license document must be a regular file")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".third-party-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if _, err = tmp.Write(updated); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("THIRD_PARTY.md changed during license collection; retry")
	}
	return os.Rename(tmp.Name(), path)
}
