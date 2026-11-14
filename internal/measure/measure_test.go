package measure

import (
	"testing"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
)

func TestNonneg(t *testing.T) {
	if nonneg(-3) != 0 || nonneg(2.5) != 2.5 {
		t.Fatal("nonneg clamps negatives only")
	}
}

// netLog folds request→response→loadingFinished into Response rows;
// this mirrors the CDP event stream without needing a browser.
func TestNetLogFolding(t *testing.T) {
	n := newNetLog()
	n.handle(&network.EventRequestWillBeSent{
		RequestID: "r1",
		Request:   &network.Request{URL: "http://x/doc"},
	})
	n.handle(&network.EventRequestWillBeSent{
		RequestID: "r2",
		Request:   &network.Request{URL: "http://x/app.js"},
	})
	if len(n.responses) != 2 {
		t.Fatalf("two requests logged: %v", n.responses)
	}

	n.handle(&network.EventResponseReceived{
		RequestID: "r1",
		Response: &network.Response{
			URL: "http://x/doc", Protocol: "h2", Status: 200,
			MimeType:          "text/html",
			EncodedDataLength: 1200,
			Headers: network.Headers{
				"Content-Encoding": "br",
				"Cache-Control":    "public, max-age=60",
				"X-Ignored":        42, // non-string dropped
			},
			Timing: &network.ResourceTiming{
				RequestTime: 1000.0,
				DNSStart:    1, DNSEnd: 5,
				ConnectStart: 5, ConnectEnd: 12,
				SslStart: 6, SslEnd: 11,
				SendStart: 12, SendEnd: 14,
				ReceiveHeadersEnd: 40,
			},
		},
	})
	r := n.responses[0]
	if r.Protocol != "h2" || r.Status != 200 || r.MimeType != "text/html" {
		t.Fatalf("response head: %+v", r)
	}
	if r.ContentEncoding != "br" || r.CacheControl != "public, max-age=60" {
		t.Fatalf("header helpers: %+v", r)
	}
	if _, ok := r.Headers["X-Ignored"]; ok {
		t.Fatal("non-string header values must be dropped")
	}
	if r.EncodedBytes != 1200 {
		t.Fatalf("encoded bytes from response head: %d", r.EncodedBytes)
	}
	if r.Timing == nil {
		t.Fatal("timing missing")
	}
	if r.Timing.DNS != 4 || r.Timing.Connect != 7 || r.Timing.SSL != 5 ||
		r.Timing.Send != 2 || r.Timing.Wait != 26 {
		t.Fatalf("waterfall phases: %+v", r.Timing)
	}

	n.handle(&network.EventLoadingFinished{
		RequestID: "r1", Timestamp: cdp.MonotonicTime(1000.1),
		EncodedDataLength: 5000,
	})
	r = n.responses[0]
	if r.EncodedBytes != 1200 {
		t.Fatalf("head bytes win over finished bytes: %d", r.EncodedBytes)
	}
	if r.Timing.Total != 100 {
		t.Fatalf("total = finish - requestTime: %v", r.Timing.Total)
	}
	// receive = total - (dns+connect+ssl+send+wait) = 100-44
	if r.Timing.Receive != 56 {
		t.Fatalf("receive: %v", r.Timing.Receive)
	}
}

func TestNetLogOrphansAndFallback(t *testing.T) {
	n := newNetLog()
	// response/finished for unknown request ids are dropped
	n.handle(&network.EventResponseReceived{RequestID: "ghost",
		Response: &network.Response{URL: "http://x"}})
	n.handle(&network.EventLoadingFinished{RequestID: "ghost"})
	if len(n.responses) != 0 {
		t.Fatal("orphan events must not create rows")
	}
	// finished supplies bytes when the response head lacked them
	n.handle(&network.EventRequestWillBeSent{RequestID: "r",
		Request: &network.Request{URL: "http://x/img.png"}})
	n.handle(&network.EventLoadingFinished{RequestID: "r",
		EncodedDataLength: 777})
	if n.responses[0].EncodedBytes != 777 {
		t.Fatalf("finished bytes fallback: %d", n.responses[0].EncodedBytes)
	}
}
