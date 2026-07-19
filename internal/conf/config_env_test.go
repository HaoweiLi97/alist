package conf

import (
	"testing"

	"github.com/caarlos0/env/v9"
)

func TestSchemeCanBeOverriddenByEnvironment(t *testing.T) {
	config := DefaultConfig()
	err := env.ParseWithOptions(config, env.Options{
		Prefix: "ALIST_",
		Environment: map[string]string{
			"ALIST_SCHEME_ADDR":       "127.0.0.1",
			"ALIST_SCHEME_HTTP_PORT":  "5250",
			"ALIST_SCHEME_HTTPS_PORT": "-1",
			"ALIST_SITE_URL":          "http://127.0.0.1:5250",
		},
	})
	if err != nil {
		t.Fatalf("parse environment: %v", err)
	}

	if config.Scheme.Address != "127.0.0.1" {
		t.Fatalf("address = %q, want 127.0.0.1", config.Scheme.Address)
	}
	if config.Scheme.HttpPort != 5250 {
		t.Fatalf("http port = %d, want 5250", config.Scheme.HttpPort)
	}
	if config.Scheme.HttpsPort != -1 {
		t.Fatalf("https port = %d, want -1", config.Scheme.HttpsPort)
	}
	if config.SiteURL != "http://127.0.0.1:5250" {
		t.Fatalf("site URL = %q", config.SiteURL)
	}
}
