package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load(t.TempDir() + "/none.yml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen == "" || c.Samples <= 0 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadYAML(t *testing.T) {
	p := t.TempDir() + "/c.yml"
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:9999\nsamples: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Listen != "127.0.0.1:9999" || c.Samples != 5 {
		t.Fatalf("load: %+v %v", c, err)
	}
}
