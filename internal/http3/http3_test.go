package http3

import (
	"context"
	"testing"
	"time"
)

func TestProbeSkipsPlainHTTP(t *testing.T) {
	r := Probe(context.Background(), "http://example.com", "")
	if r.Probed || r.Served || r.Error != "" {
		t.Fatalf("http target must not be QUIC-probed: %+v", r)
	}
}

func TestProbeAdvertisedFlag(t *testing.T) {
	r := Probe(context.Background(), "http://example.com", `h3=":443"; ma=2592000`)
	if !r.Advertised {
		t.Fatal("alt-svc h3 should set Advertised")
	}
	if r.Probed {
		t.Fatal("advertised is header-derived, not a QUIC attempt")
	}
}

func TestProbeHTTPSUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// loopback :1 — nothing listens; QUIC must fail fast, bounded by ctx.
	r := Probe(ctx, "https://127.0.0.1:1", "")
	if !r.Probed {
		t.Fatal("https target should attempt a QUIC probe")
	}
	if r.Served {
		t.Fatal("closed port must not report served")
	}
	if r.Error == "" {
		t.Fatal("want error message for failed probe")
	}
}
