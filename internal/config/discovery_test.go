package config

import (
	"slices"
	"testing"
	"time"
)

func TestLoadDiscovery(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		enabled bool
	}{
		{"off", nil, false, false},
		{"on", map[string]string{"SERVICE_DISCOVERY_SECRET": secret, "SERVICE_URL": "http://units:8080", "AMQP_URL": "amqp://rabbit"}, false, true},
		{"secret without broker", map[string]string{"SERVICE_DISCOVERY_SECRET": secret, "SERVICE_URL": "http://units:8080"}, false, false},
		{"short secret", map[string]string{"SERVICE_DISCOVERY_SECRET": "short", "SERVICE_URL": "http://units:8080"}, true, false},
		{"no URL", map[string]string{"SERVICE_DISCOVERY_SECRET": secret}, true, false},
		{"bad URL", map[string]string{"SERVICE_DISCOVERY_SECRET": secret, "SERVICE_URL": "units:8080"}, true, false},
		{"bad broker URL", map[string]string{"AMQP_URL": "http://rabbit"}, true, false},
		{"short token TTL", map[string]string{"SERVICE_TOKEN_TTL": "30m"}, true, false},
		{"short announce interval", map[string]string{"SERVICE_ANNOUNCE_INTERVAL": "10ms"}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"AMQP_URL", "SERVICE_DISCOVERY_SECRET", "SERVICE_URL", "SERVICE_TOKEN_TTL", "SERVICE_TOKEN_CLIENTS", "SERVICE_ANNOUNCE_INTERVAL"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			d, err := loadDiscovery()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %t", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if d.Enabled() != tc.enabled {
				t.Errorf("Enabled() = %t, want %t", d.Enabled(), tc.enabled)
			}
			if !slices.Equal(d.Clients, []string{"api"}) || d.TokenTTL != 168*time.Hour || d.AnnounceInterval != time.Minute {
				t.Errorf("defaults = %+v", d)
			}
		})
	}
}
