package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type archiveTransport map[string][]byte

func (a archiveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := a[req.URL.String()]
	if !ok {
		return nil, errors.New("fixture download failed")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
}

func licenseArchive(t *testing.T, files []licenseFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, file := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "package/" + file.name, Mode: 0644, Size: int64(len(file.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func archiveIntegrity(data []byte) string {
	sha := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sha[:])
}

func writeFixture(t *testing.T, name string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(name, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

// Local tar/gzip fixtures reuse the audited document's legal bytes; no second
// collection of license texts or network service is needed. Only the temporary
// lock and source manifest change to pin these synthetic archive bytes.
func licensesFixture(t *testing.T) (string, []byte, archiveTransport) {
	t.Helper()
	doc, err := os.ReadFile("../../THIRD_PARTY.md")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := bundlerFixture(t)
	writeFixture(t, filepath.Join(root, "../../THIRD_PARTY.md"), doc)
	lockPath := filepath.Join(root, "upstream/linkedom-runtime-lock.json")
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var packages []component
	if err := json.Unmarshal(raw, &packages); err != nil {
		t.Fatal(err)
	}
	archives := archiveTransport{}
	legalFile := regexp.MustCompile(`(?m)^--- (.+) ---\n`)
	for i, p := range packages {
		body := noticeBlock(t, doc, licenseHeading(p))
		matches := legalFile.FindAllStringSubmatchIndex(body, -1)
		var files []licenseFile
		for j, match := range matches {
			end := len(body)
			if j+1 < len(matches) {
				end = matches[j+1][0] - 1 // separator newline
			}
			files = append(files, licenseFile{body[match[2]:match[3]], body[match[1]:end]})
		}
		if len(files) == 0 {
			t.Fatalf("no fixture legal files for %s", p.Name)
		}
		archives[p.URL] = licenseArchive(t, files)
		packages[i].Integrity = archiveIntegrity(archives[p.URL])
	}
	raw, _ = json.Marshal(packages)
	writeFixture(t, lockPath, raw)
	pins := map[string]string{}
	rawPins, err := os.ReadFile(filepath.Join(root, "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawPins, &pins); err != nil {
		t.Fatal(err)
	}
	pins["upstream/linkedom-runtime-lock.json"] = fmt.Sprintf("%x", sha256.Sum256(raw))
	rawPins, _ = json.Marshal(pins)
	writeFixture(t, filepath.Join(root, "sources.json"), rawPins)
	previous := http.DefaultTransport
	http.DefaultTransport = archives
	t.Cleanup(func() { http.DefaultTransport = previous })
	return root, doc, archives
}

func noticeBlock(t *testing.T, doc []byte, heading string) string {
	t.Helper()
	_, section, found := strings.Cut(string(doc), heading+"\n")
	if !found {
		t.Fatalf("missing notice section: %s", heading)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	_, section, found = strings.Cut(section, "```text\n")
	if !found {
		t.Fatalf("missing notice text: %s", heading)
	}
	body, _, found := strings.Cut(section, "```\n")
	if !found {
		t.Fatalf("missing notice fence: %s", heading)
	}
	return body
}

type licenseFileState struct {
	body string
	info os.FileInfo
}

func licenseTree(t *testing.T, root string) map[string]licenseFileState {
	t.Helper()
	state := map[string]licenseFileState{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state[path] = licenseFileState{string(body), info}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertLicenseTree(t *testing.T, before map[string]licenseFileState, root string) {
	t.Helper()
	after := licenseTree(t, root)
	if len(after) != len(before) {
		t.Fatal("license command changed file inventory")
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok || want.body != got.body || !want.info.ModTime().Equal(got.info.ModTime()) || want.info.Mode() != got.info.Mode() {
			t.Fatalf("license command modified %s", path)
		}
	}
}

func licenseCommand(t *testing.T, mode string, success bool, contains string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := run([]string{mode}, &out, &errs)
	if (code == 0) != success || !strings.Contains(errs.String(), contains) {
		t.Fatalf("%s returned %d: %s", mode, code, errs.String())
	}
}

func TestLicensesPreservationAndCheck(t *testing.T) {
	root, original, _ := licensesFixture(t)
	path := filepath.Join(root, "../../THIRD_PARTY.md")
	// Include CRLF and arbitrary manual bytes on both sides of the markers.
	original = append([]byte("manual preface\r\n"), original...)
	original = append(original, []byte("\r\nmanual afterword\r\n")...)
	start, end, err := licenseBounds(original)
	if err != nil {
		t.Fatal(err)
	}
	stale := bytes.Replace(original, []byte("Copyright (c) Felix Böhm"), []byte("STALE GENERATED TEXT"), 1)
	writeFixture(t, path, stale)
	licenseCommand(t, "-licenses", true, "")
	generated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newStart, newEnd, err := licenseBounds(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original[:start], generated[:newStart]) || !bytes.Equal(original[end:], generated[newEnd:]) {
		t.Fatal("generation changed manual bytes")
	}
	verifyAuditedNotices(t, generated)
	before := licenseTree(t, filepath.Join(root, "../.."))
	licenseCommand(t, "-licenses", true, "")
	licenseCommand(t, "-licenses-check", true, "")
	assertLicenseTree(t, before, filepath.Join(root, "../.."))
	if !bytes.Equal(original, generated) {
		t.Fatal("local fixture regeneration differs from audited document")
	}
	for _, mutation := range []string{"license", "missing package", "extra section", "reordered"} {
		t.Run(mutation, func(t *testing.T) {
			bad := append([]byte(nil), generated...)
			switch mutation {
			case "license":
				bad = bytes.Replace(bad, []byte("Copyright (c) Felix Böhm"), []byte("changed copyright"), 1)
			case "missing package":
				i := bytes.Index(bad, []byte("## npm: boolbase 2.0.0\n"))
				j := bytes.Index(bad, []byte("## npm: css-select 7.0.0\n"))
				bad = append(bad[:i], bad[j:]...)
			case "extra section":
				bad = bytes.Replace(bad, []byte(licensesEnd), []byte("extra section\n"+licensesEnd), 1)
			case "reordered":
				i := bytes.Index(bad, []byte("## npm: boolbase 2.0.0\n"))
				j := bytes.Index(bad, []byte("## npm: css-select 7.0.0\n"))
				k := bytes.Index(bad, []byte("## npm: css-what 8.0.0\n"))
				first, second := string(bad[i:j]), string(bad[j:k])
				bad = []byte(string(bad[:i]) + second + first + string(bad[k:]))
			}
			writeFixture(t, path, bad)
			before := licenseTree(t, filepath.Join(root, "../.."))
			want := "generated licenses differ"
			if mutation == "license" {
				want += ": css-select 7.0.0"
			}
			if mutation == "missing package" {
				want += ": boolbase 2.0.0"
			}
			licenseCommand(t, "-licenses-check", false, want)
			assertLicenseTree(t, before, filepath.Join(root, "../.."))
		})
	}
}

func TestLicensesBadMarkers(t *testing.T) {
	for _, bad := range []string{
		"no markers", licensesBegin, licensesEnd,
		licensesEnd + "\n" + licensesBegin,
		licensesBegin + "\n" + licensesBegin + "\n" + licensesEnd,
		licensesBegin + "\n" + licensesEnd + "\n" + licensesEnd,
		"prefix " + licensesBegin + "\n" + licensesEnd,
		licensesBegin + " trailing text\n" + licensesEnd,
		"<!-- BEGIN GENERATED LICENSES --\n" + licensesEnd,
		licensesBegin + "\n<!-- END GENERATED LICENSES --",
	} {
		t.Run(bad, func(t *testing.T) {
			root, _, archives := licensesFixture(t)
			clear(archives) // marker errors must be reported before any download
			writeFixture(t, filepath.Join(root, "../../THIRD_PARTY.md"), []byte(bad))
			before := licenseTree(t, filepath.Join(root, "../.."))
			for _, mode := range []string{"-licenses", "-licenses-check"} {
				licenseCommand(t, mode, false, "markers")
				assertLicenseTree(t, before, filepath.Join(root, "../.."))
			}
		})
	}
}

func TestLicensesCollectionFailureAtomicity(t *testing.T) {
	for _, failure := range []string{"download", "integrity", "no license", "source pin"} {
		t.Run(failure, func(t *testing.T) {
			root, doc, archives := licensesFixture(t)
			writeFixture(t, filepath.Join(root, "../../THIRD_PARTY.md"), bytes.Replace(doc, []byte("Copyright (c) Felix Böhm"), []byte("stale"), 1))
			// Fail the final sorted package, after collecting eighteen successfully.
			url := "https://registry.npmjs.org/uhyphen/-/uhyphen-0.2.0.tgz"
			want := "uhyphen 0.2.0"
			switch failure {
			case "download":
				delete(archives, url)
			case "integrity":
				archives[url] = []byte("tampered archive")
			case "no license":
				archives[url] = licenseArchive(t, []licenseFile{{"README", "not a license"}})
				lockPath := filepath.Join(root, "upstream/linkedom-runtime-lock.json")
				raw, _ := os.ReadFile(lockPath)
				var packages []component
				json.Unmarshal(raw, &packages)
				for i := range packages {
					if packages[i].URL == url {
						packages[i].Integrity = archiveIntegrity(archives[url])
					}
				}
				raw, _ = json.Marshal(packages)
				writeFixture(t, lockPath, raw)
				rawPins, _ := json.Marshal(map[string]string{"upstream/linkedom-runtime-lock.json": fmt.Sprintf("%x", sha256.Sum256(raw))})
				writeFixture(t, filepath.Join(root, "sources.json"), rawPins)
			case "source pin":
				writeFixture(t, filepath.Join(root, "upstream/linkedom-runtime-lock.json"), []byte("[]"))
				want = "source integrity mismatch"
			}
			before := licenseTree(t, filepath.Join(root, "../.."))
			for _, mode := range []string{"-licenses", "-licenses-check"} {
				licenseCommand(t, mode, false, want)
				assertLicenseTree(t, before, filepath.Join(root, "../.."))
			}
		})
	}
}

func TestLicenseArchiveRendering(t *testing.T) {
	files := []licenseFile{{"NOTICE", "Attribution\r\n```\r\n"}, {"LICENSE", "Grant\rConditions\r\nDisclaimer\n"}, {"nested/LICENSE", "not top-level"}}
	data := licenseArchive(t, files)
	p := component{"fixture", "1.2.3", "https://registry.npmjs.org/fixture.tgz", archiveIntegrity(data), "MIT"}
	client := &http.Client{Transport: archiveTransport{p.URL: data}}
	notice, err := collectPackageLicense(p, client)
	if err != nil {
		t.Fatal(err)
	}
	want := []licenseFile{{"LICENSE", "Grant\nConditions\nDisclaimer\n"}, {"NOTICE", "Attribution\n```\n"}}
	if !reflect.DeepEqual(notice.files, want) {
		t.Fatalf("legal files: %#v", notice.files)
	}
	rendered := renderLicense(notice)
	if !strings.Contains(rendered, "````text\n") || strings.Contains(rendered, "\r") || !strings.Contains(rendered, "Attribution\n```\n") {
		t.Fatal(rendered)
	}
	if _, err := readLimited(strings.NewReader("oversized"), 3); err == nil {
		t.Fatal("oversized legal text accepted")
	}
	for _, files := range [][]licenseFile{{{"LICENSE", ""}}, {{"LICENSE", "a"}, {"LICENSE", "b"}}, {{"../LICENSE", "path traversal"}}} {
		data := licenseArchive(t, files)
		p.Integrity = archiveIntegrity(data)
		client.Transport = archiveTransport{p.URL: data}
		if _, err := collectPackageLicense(p, client); err == nil {
			t.Fatalf("invalid legal files accepted: %#v", files)
		}
	}
}

func TestLicenseExclusiveModes(t *testing.T) {
	for _, args := range [][]string{{"-check", "-licenses-check"}, {"-licenses", "-licenses-check"}, {"-check", "-licenses", "-licenses-check"}, {"-licenses-check", "unexpected"}} {
		var out, errs bytes.Buffer
		if code := run(args, &out, &errs); code != 2 {
			t.Fatalf("invalid arguments %v returned %d", args, code)
		}
	}
}

func TestLicenseAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "THIRD_PARTY.md")
	writeFixture(t, path, []byte("manual concurrent edit"))
	if err := replaceLicenseDocument(path, []byte("outdated original"), []byte("replacement")); err == nil {
		t.Fatal("overwrote concurrent edit")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "manual concurrent edit" {
		t.Fatal("concurrent edit damaged")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if err := replaceLicenseDocument(path, got, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("replacement changed file permissions")
	}
}

// Explicit preservation inventory from LICENSING_PROVENANCE_AUDIT.md §3 and
// LICENSING_IMPLEMENTATION_REPORT.md §3. Hashes pin all 22 migrated notice files
// plus the two existing full MIT blocks, normalizing only final LF for fencing.
// This test inventory deliberately detects a lockfile or document that loses a
// previously audited component; it is not an additional production inventory.
func verifyAuditedNotices(t *testing.T, doc []byte) {
	t.Helper()
	required := []struct {
		heading, sha256 string
		generated       bool
	}{
		{"## BgUtils 4.0.3 — MIT", "69bb14fee2d102583d6cccdf1e0e2ced4d2bbfd3c42b172e0a9f4e32a9c48c88", false},
		{"## npm: boolbase 2.0.0", "f31876253a77d3ab4ae77bb2fe75573b447ec7ab7a10a00c569aaa9c1091a43b", true},
		{"## npm: css-select 7.0.0", "0a1bbb164ac449c0db8670c92fca0d247fbf52f4588c584c7ff03307d219a777", true},
		{"## npm: css-what 8.0.0", "804fe5c510314f44c014f09ed8ae7357a2b5a558da20545e66755f6359a30cca", true},
		{"## npm: cssomnom 0.1.11", "ac7f39d7ed06bb7141909417b04efba17aa18307d9a34c6feabb2385b19f538f", true},
		{"## npm: dom-serializer 2.0.0", "b7e405296d5d04e2001a2f3f151c0eb5fdd1596aeb8bb94eb81f38a0aab73953", true},
		{"## npm: dom-serializer 3.1.1", "84d839ad9531069d249632186d8ff7a32601afe02f36a3cb14f7fd716ac2a090", true},
		{"## npm: domelementtype 2.3.0", "7dc005f16210024b5c00f397ccc1c83b74db839b3a860be343dcd6755a5a20ab", true},
		{"## npm: domelementtype 3.0.0", "423d8d02de5d5d3ed26220d11e8033c285d81f34be57a14466a3f12ad9c5c4a4", true},
		{"## npm: domhandler 5.0.3", "92aaf1656d0b9d4b1e4ce731ebe950b3e5aaffcb5183b60a92c44b61c9cfcbbd", true},
		{"## npm: domhandler 6.0.1", "a2e8ba494c70d2691ffcf66abe7eb93112eb164b93a5217c4c0387151cbbe000", true},
		{"## npm: domutils 3.2.2", "2dcda432a0d570b88d1e0506647ba13bea8ce762868cbd38bb50628538f1388b", true},
		{"## npm: domutils 4.0.2", "8a0151f4ddb181b6c680360cd6e92b623b8f8e1e34e30cf01a5dd11144bf36a3", true},
		{"## npm: entities 4.5.0", "7af7e40b601d38b4021bf83d9ca4c955d988b97c42f8dbb2b51080c5ab917e2f", true},
		{"## npm: entities 7.0.1", "dde8c7ec35217d56301742268fc47154078eb5c079e2700295078654714b1b57", true},
		{"## npm: entities 8.1.0", "30053d517590b92c9a990927fb5cf6e5c8b81608ca9c62ad7831ee1247336a3b", true},
		{"## npm: html-escaper 3.0.3", "5076342e61c1d3f4ff08af7705d1c7501f0ea3d74965b2bbab4d9c2bd25d2f69", true},
		{"## npm: htmlparser2 10.1.0", "86e89644daac787849d473fb269183168bb9c241bce93969664d601ab86f00aa", true},
		{"## npm: nth-check 3.0.1", "e50a50f4efa49e5301484ea339ef6e0179aa328f6a1e079cc0b67eb6f861f9c7", true},
		{"## npm: uhyphen 0.2.0", "6b593af427aa384203c8edee4ed9333cbf72b70087fd066612cf74e9b613a8e0", true},
		{"## XRegExp escaping fragment — MIT", "12e41e85db526a060cb593f615e9fd1d8d6983cb0385669e24c47d0ace72fb51", false},
		{"## LinkeDOM 0.18.15 — ISC", "dc6d4961d8b6ee747231582ae9c53ce1d66bf76bc9f5a28f554c0e97210953bf", false},
		{"## esbuild injected helpers — MIT", "b40ec5baec7bb34fa5b1c09521fa3cd52d5fad7adafed74932a2010d3612a681", false},
		{"## Sizzle unescape fragment — historical MIT notice", "244779275ccf7def02a7724a707dfe8787f83afd5ccb6498b42296ae42484940", false},
	}
	start, end, err := licenseBounds(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range required {
		body := noticeBlock(t, doc, required.heading)
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		if got != required.sha256 {
			t.Errorf("audited license changed: %s (%s)", required.heading, got)
		}
		location := bytes.Index(doc, []byte(required.heading+"\n"))
		if (location >= start && location < end) != required.generated {
			t.Errorf("wrong manual/generated placement: %s", required.heading)
		}
		if bytes.Count(doc, []byte(required.heading+"\n")) != 1 {
			t.Errorf("duplicate component section: %s", required.heading)
		}
	}
	if !bytes.Contains(doc[:start], []byte("Copyright 2026 Google LLC")) {
		t.Error("lost CSSomnom source-header attribution")
	}
}

func TestAuditedLicenseInventory(t *testing.T) {
	doc, err := os.ReadFile("../../THIRD_PARTY.md")
	if err != nil {
		t.Fatal(err)
	}
	verifyAuditedNotices(t, doc)
	internal := regexp.MustCompile(`(?i)\b(?:H[123]|M[1-8]|Astra|Sol|Codex|blockers?|closure|owner.authoriz|audit status)\b`)
	if internal.Match(doc) {
		t.Fatal("public license document contains internal development terminology")
	}
}
