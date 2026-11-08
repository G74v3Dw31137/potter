package botguard

import (
	"net/url"
	"strings"
)

// resolveDOMURL supplies URL attribute reflection in the virtual HTML document.
// It performs no I/O. net/url covers relative resolution and escaping, but is
// not a complete WHATWG URL parser (notably IDNA and legacy IPv4 syntax).
// Unparseable values are reflected verbatim, as with invalid browser URLs.
func resolveDOMURL(value, base string) string {
	clean := func(s string) string {
		s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
		return strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)
	}
	b, err := url.Parse(clean(base))
	if err != nil {
		return value
	}
	u, err := url.Parse(clean(value))
	if err != nil {
		return value
	}
	u = b.ResolveReference(u)
	if u.Scheme == "" || (u.Opaque == "" && u.Host == "" && b.Opaque != "") {
		return value
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ftp" {
		if u.Host == "" {
			return value
		}
		port := map[string]string{"http": "80", "https": "443", "ftp": "21"}[u.Scheme]
		if u.Port() == port {
			u.Host = strings.TrimSuffix(u.Host, ":"+port)
		}
		if u.Path == "" {
			u.Path = "/"
		}
	}
	// URL.String escapes paths/fragments, but leaves the raw query unchanged.
	var query strings.Builder
	const hex = "0123456789ABCDEF"
	for _, c := range []byte(u.RawQuery) {
		if c <= 0x20 || c >= 0x7f || c == '"' || c == '<' || c == '>' || (c == '\'' && (u.Scheme == "http" || u.Scheme == "https")) {
			query.WriteByte('%')
			query.WriteByte(hex[c>>4])
			query.WriteByte(hex[c&15])
		} else {
			query.WriteByte(c)
		}
	}
	u.RawQuery = query.String()
	return u.String()
}
