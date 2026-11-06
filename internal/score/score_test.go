package score

import (
	"strings"
	"testing"
)

func TestBadgeSVG(t *testing.T) {
	svg := BadgeSVG("lcp", "1.2s", Good)
	if !strings.Contains(svg, "<svg") || !strings.Contains(svg, "lcp") {
		t.Fatalf("badge: %s", svg)
	}
	bad := BadgeSVG("x", "<script>", Poor)
	if strings.Contains(bad, "<script>") {
		t.Fatal("badge must escape input")
	}
}

func TestMetricBand(t *testing.T) {
	if Metric("lcp", 2000) != Good || Metric("lcp", 3000) != OK || Metric("lcp", 5000) != Poor {
		t.Fatal("lcp band thresholds")
	}
	if Metric("nope", 1) != "" {
		t.Fatal("unknown metric must be empty band")
	}
}
