package scheduler

import (
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0}, {"manual", 0}, {"daily", 24 * time.Hour},
		{"30m", 30 * time.Minute}, {"6h", 6 * time.Hour},
	} {
		got, err := ParseInterval(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q → %v %v, want %v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseInterval("bogus"); err == nil {
		t.Error("bogus interval should error")
	}
}
