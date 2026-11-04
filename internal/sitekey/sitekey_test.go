package sitekey

import "testing"

func TestFrom(t *testing.T) {
	cases := []struct{ in, key, canon string }{
		{"localhost:3000", "localhost:3000", "http://localhost:3000"},
		{"http://localhost:3000", "localhost:3000", "http://localhost:3000"},
		{"http://localhost:3000/", "localhost:3000", "http://localhost:3000"},
		{"HTTP://LOCALHOST:3000/", "localhost:3000", "http://localhost:3000"},
		{"https://example.com", "example.com", "https://example.com"},
		{"https://example.com:443/x", "example.com/x", "https://example.com/x"},
		{"http://example.com:8080", "example.com:8080", "http://example.com:8080"},
		{"192.168.1.10:8080/app", "192.168.1.10:8080/app", "http://192.168.1.10:8080/app"},
		{"localhost:3000/blog/", "localhost:3000/blog", "http://localhost:3000/blog"},
	}
	for _, c := range cases {
		k, canon, err := From(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if k != c.key || canon != c.canon {
			t.Errorf("%s → key=%q canon=%q want %q %q", c.in, k, canon, c.key, c.canon)
		}
	}
}
