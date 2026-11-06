// Package score maps metrics to web.dev bands: good/ok/poor.
package score

// Band is a web.dev rating band for a metric value.
type Band string

// Rating bands.
const (
	Good Band = "good"
	OK   Band = "ok"
	Poor Band = "poor"
)

var bands = map[string][2]float64{
	"ttfb": {800, 1800}, "fcp": {1800, 3000}, "lcp": {2500, 4000},
	"cls": {0.1, 0.25}, "tbt": {200, 600}, "inp": {200, 500},
}

// Metric rates a value for id ("lcp","cls",...). Unknown → "".
func Metric(id string, v float64) Band {
	b, ok := bands[id]
	if !ok {
		return ""
	}
	switch {
	case v > b[1]:
		return Poor
	case v > b[0]:
		return OK
	}
	return Good
}
