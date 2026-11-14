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

func TestEnvOverrides(t *testing.T) {
	t.Setenv("WVF_LISTEN", "0.0.0.0:1234")
	t.Setenv("WVF_STATE_DB", "/tmp/wvf-env.db")
	t.Setenv("WVF_BROWSER_PATH", "/opt/chrome")
	t.Setenv("WVF_PSI_KEY", "K")
	t.Setenv("WVF_SAMPLES", "7")
	c, err := Load(t.TempDir() + "/none.yml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:1234" || c.StateDB != "/tmp/wvf-env.db" ||
		c.BrowserPath != "/opt/chrome" || c.PSIKey != "K" || c.Samples != 7 {
		t.Fatalf("env overrides: %+v", c)
	}
}

func TestEnvSamplesNonNumeric(t *testing.T) {
	t.Setenv("WVF_SAMPLES", "bogus")
	c, err := Load(t.TempDir() + "/none.yml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Samples <= 0 {
		t.Fatalf("non-numeric WVF_SAMPLES must keep default: %d", c.Samples)
	}
}

func TestLoadBadYAML(t *testing.T) {
	p := t.TempDir() + "/bad.yml"
	if err := os.WriteFile(p, []byte("listen: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("malformed yaml should error")
	}
}
