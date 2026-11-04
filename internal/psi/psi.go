// Package psi is the opt-in PageSpeed Insights layer — public FQDNs
// only, field (CrUX) + lab data kept separate from local runs.
package psi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const endpoint = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed"

// Result is the slice we keep from a PSI response.
type Result struct {
	FieldData bool               `json:"field_data"` // CrUX real-user data present
	P75       map[string]float64 `json:"p75,omitempty"`
	PerfScore float64            `json:"perf_score"`
	LabLCPms  *float64           `json:"lab_lcp_ms,omitempty"`
	LabTBTms  *float64           `json:"lab_tbt_ms,omitempty"`
	LabCLS    *float64           `json:"lab_cls,omitempty"`
}

type psiJSON struct {
	LoadingExperience struct {
		Metrics map[string]struct {
			Percentile float64 `json:"percentile"`
		} `json:"metrics"`
	} `json:"loadingExperience"`
	LighthouseResult struct {
		Categories map[string]struct {
			Score *float64 `json:"score"`
		} `json:"categories"`
		Audits map[string]struct {
			NumericValue *float64 `json:"numericValue"`
		} `json:"audits"`
	} `json:"lighthouseResult"`
}

// Fetch calls PSI for url. apiKey may be "" (unauthenticated quota).
// Fails cleanly on localhost/private targets — PSI can't reach them.
func Fetch(ctx context.Context, rawurl, apiKey string) (*Result, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("url", rawurl)
	q.Set("strategy", "mobile")
	if apiKey != "" {
		q.Set("key", apiKey)
	}
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("psi %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	var raw psiJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	r := &Result{P75: map[string]float64{}}
	r.FieldData = len(raw.LoadingExperience.Metrics) > 0
	for k, m := range raw.LoadingExperience.Metrics {
		r.P75[k] = m.Percentile
	}
	if c := raw.LighthouseResult.Categories["performance"]; c.Score != nil {
		r.PerfScore = *c.Score * 100
	}
	get := func(k string) *float64 {
		a := raw.LighthouseResult.Audits[k]
		if a.NumericValue == nil {
			return nil
		}
		v := *a.NumericValue
		return &v
	}
	r.LabLCPms = get("largest-contentful-paint")
	r.LabTBTms = get("total-blocking-time")
	r.LabCLS = get("cumulative-layout-shift")
	return r, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
