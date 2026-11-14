// Package http3 directly probes whether a host answers QUIC/HTTP3 —
// distinguishes "advertised via alt-svc" from "actually serves h3".
package http3

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quic-go/quic-go/http3"
)

// Result reports the h3 probe outcome for a target.
type Result struct {
	Advertised bool   `json:"advertised"` // alt-svc header present
	Probed     bool   `json:"probed"`     // direct QUIC request attempted
	Served     bool   `json:"served"`     // QUIC request succeeded
	Error      string `json:"error,omitempty"`
}

// Probe attempts an HTTP/3 request to the target's origin. It always
// tries when the scheme is https — alt-svc may be absent yet h3 served.
func Probe(ctx context.Context, rawurl, altSvc string) *Result {
	r := &Result{Advertised: strings.Contains(altSvc, "h3")}
	u, err := url.Parse(rawurl)
	if err != nil || u.Scheme != "https" {
		return r
	}
	r.Probed = true
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{
			ServerName: u.Hostname(),
			MinVersion: tls.VersionTLS13, // QUIC requires TLS 1.3
		},
	}
	defer func() { _ = tr.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+u.Host+"/", nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		r.Error = fmt.Sprintf("h3 probe failed: %v", err)
		return r
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	r.Served = resp.ProtoMajor == 3
	return r
}
