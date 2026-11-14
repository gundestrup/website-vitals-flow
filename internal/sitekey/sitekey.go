// Package sitekey normalizes a URL into its stable site identity —
// "localhost:3000", "example.com", "192.168.1.10:8080", with path
// variants collapsing so history never splits.
package sitekey

import (
	"net"
	"net/url"
	"strings"
)

// From returns (key, canonicalURL). key = host[:port][/path], lowercased,
// no trailing slash, default ports dropped. canonicalURL includes scheme.
func From(raw string) (key, canonical string, err error) {
	u := raw
	if !strings.Contains(u, "://") {
		u = "http://" + u // NOSONAR -- scheme default for canonicalization; http targets (localhost dev servers) are supported by design
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return "", "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", "", errInvalid(raw)
	}
	port := parsed.Port()
	if port == "80" && parsed.Scheme == "http" || port == "443" && parsed.Scheme == "https" {
		port = ""
	}
	key = host
	if port != "" {
		key += ":" + port
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if path != "" {
		key += path
	}
	scheme := parsed.Scheme
	hostport := host
	if port != "" {
		hostport = net.JoinHostPort(host, port)
	}
	return key, scheme + "://" + hostport + path, nil
}

func errInvalid(s string) error {
	return &url.Error{Op: "sitekey", URL: s, Err: errEmptyHost{}}
}

type errEmptyHost struct{}

func (errEmptyHost) Error() string { return "empty host" }
