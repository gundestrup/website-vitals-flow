// Package httpcheck runs HTTP-level probes: compression negotiation,
// redirects, TLS, alt-svc, and RFC 7234 cacheability verdicts.
package httpcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pquerna/cachecontrol"
)

// EncodingProbe is the outcome of one Accept-Encoding offer.
type EncodingProbe struct {
	Offered  string `json:"offered"`
	Returned string `json:"returned,omitempty"`
	Bytes    int64  `json:"bytes"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

// CacheVerdict is the RFC 7234 evaluation of one response.
type CacheVerdict struct {
	URL       string   `json:"url"`
	Cacheable bool     `json:"cacheable"`
	Reasons   []string `json:"reasons,omitempty"`
	Expires   string   `json:"expires,omitempty"`
}

// Result is the complete HTTP-check payload for a target.
type Result struct {
	URL        string                    `json:"url"`
	FinalURL   string                    `json:"final_url"`
	Status     int                       `json:"status"`
	Redirects  []string                  `json:"redirects,omitempty"`
	TLSVersion string                    `json:"tls_version,omitempty"`
	Server     string                    `json:"server,omitempty"`
	AltSvc     string                    `json:"alt_svc,omitempty"`
	Encodings  map[string]*EncodingProbe `json:"encodings"`
	Cache      []CacheVerdict            `json:"cache,omitempty"`
	Basics     map[string]int            `json:"basics,omitempty"` // path -> status
}

// Asset is one response's cache-relevant headers (from the CDP layer).
type Asset struct {
	URL     string
	Status  int64
	Headers map[string]string
}

var offers = []string{"br", "zstd", "gzip", "identity"}

// Probe fetches rawurl once per offered encoding plus a tracking GET.
// Headers+size only by default — no body decode (CDP shows real sizes).
func Probe(ctx context.Context, rawurl string) (*Result, error) {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{DisableCompression: true},
	}
	res := &Result{URL: rawurl, Encodings: map[string]*EncodingProbe{}}

	var redirects []string
	var lastResp *http.Response
	track := *client
	track.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		redirects = append(redirects, req.URL.String())
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	lastResp, err = track.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawurl, err)
	}
	io.Copy(io.Discard, lastResp.Body)
	lastResp.Body.Close()
	res.Status = lastResp.StatusCode
	res.FinalURL = lastResp.Request.URL.String()
	res.Redirects = redirects
	res.Server = lastResp.Header.Get("Server")
	res.AltSvc = lastResp.Header.Get("Alt-Svc")
	if lastResp.TLS != nil {
		res.TLSVersion = tls.VersionName(lastResp.TLS.Version)
	}

	for _, enc := range offers {
		res.Encodings[enc] = probeEncoding(ctx, client, res.FinalURL, enc)
	}
	res.Basics = probeBasics(ctx, client, res.FinalURL)
	return res, nil
}

var basicPaths = []string{
	"/robots.txt", "/sitemap.xml", "/favicon.ico", "/site.webmanifest",
}

// probeBasics HEAD-checks conventional site files — cheap presence
// signals for the "basics" best-practice group.
func probeBasics(ctx context.Context, client *http.Client, finalURL string) map[string]int {
	out := map[string]int{}
	for _, p := range basicPaths {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead,
			strings.TrimSuffix(finalURL, "/")+p, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			out[p] = 0
			continue
		}
		resp.Body.Close()
		out[p] = resp.StatusCode
	}
	return out
}

func probeEncoding(ctx context.Context, client *http.Client, url, enc string) *EncodingProbe {
	p := &EncodingProbe{Offered: enc}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	if enc != "identity" {
		req.Header.Set("Accept-Encoding", enc)
	} else {
		req.Header.Set("Accept-Encoding", "identity")
	}
	resp, err := client.Do(req)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	defer resp.Body.Close()
	n, _ := io.CopyN(io.Discard, resp.Body, 8<<20)
	p.Bytes = n
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		var c int64
		if _, err := fmt.Sscanf(cl, "%d", &c); err == nil && c > n {
			p.Bytes = c
		}
	}
	p.Returned = resp.Header.Get("Content-Encoding")
	p.OK = enc == "identity" || strings.EqualFold(p.Returned, enc)
	if enc == "identity" {
		p.OK = p.Returned == "" || strings.EqualFold(p.Returned, "identity")
	}
	return p
}

// CacheVerdicts evaluates each asset's cache headers per RFC 7234.
func CacheVerdicts(assets []Asset) []CacheVerdict {
	var out []CacheVerdict
	for _, a := range assets {
		req, err := http.NewRequest(http.MethodGet, a.URL, nil)
		if err != nil {
			continue
		}
		h := make(http.Header, len(a.Headers))
		for k, v := range a.Headers {
			h.Set(k, v)
		}
		reasons, expires, err := cachecontrol.CachableResponse(req, &http.Response{
			StatusCode: int(a.Status),
			Header:     h,
		}, cachecontrol.Options{})
		if err != nil {
			continue
		}
		v := CacheVerdict{URL: a.URL, Cacheable: len(reasons) == 0}
		for _, r := range reasons {
			v.Reasons = append(v.Reasons, r.String())
		}
		if !expires.IsZero() {
			v.Expires = expires.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}
