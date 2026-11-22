package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"go-unit-mangement/internal/servicebus"
)

// Discovery lets this server announce itself on the message broker
// (AMQPURL) and hand API tokens to the services in Clients, so they find it
// without a hand-made token. An empty Secret turns it off.
type Discovery struct {
	// AMQPURL is amqp://user:pass@host:5672/vhost or amqps://… for TLS.
	AMQPURL string
	// Secret is shared by every service; messages not signed with it are
	// ignored.
	Secret string
	// URL is where the other services reach this server, e.g.
	// http://units:8080.
	URL string
	// TokenTTL is how long an issued token lasts; the services renew it at
	// half its lifetime.
	TokenTTL time.Duration
	// Clients are the services that may get a token.
	Clients          []string
	AnnounceInterval time.Duration
}

// Enabled reports whether this server takes part in service discovery.
func (d Discovery) Enabled() bool { return d.Secret != "" && d.AMQPURL != "" }

// loadDiscovery reads AMQP_URL, SERVICE_DISCOVERY_SECRET, SERVICE_URL,
// SERVICE_TOKEN_TTL, SERVICE_TOKEN_CLIENTS and SERVICE_ANNOUNCE_INTERVAL.
func loadDiscovery() (Discovery, error) {
	d := Discovery{
		AMQPURL: strings.TrimSpace(os.Getenv("AMQP_URL")),
		Secret:  strings.TrimSpace(os.Getenv("SERVICE_DISCOVERY_SECRET")),
		URL:     strings.TrimSpace(os.Getenv("SERVICE_URL")),
		Clients: splitList(getEnv("SERVICE_TOKEN_CLIENTS", "api")),
	}
	if d.AMQPURL != "" && !strings.HasPrefix(d.AMQPURL, "amqp://") && !strings.HasPrefix(d.AMQPURL, "amqps://") {
		return Discovery{}, errors.New("AMQP_URL must start with amqp:// or amqps://")
	}
	var err error
	if d.TokenTTL, err = time.ParseDuration(getEnv("SERVICE_TOKEN_TTL", "168h")); err != nil || d.TokenTTL < time.Hour {
		return Discovery{}, errors.New("SERVICE_TOKEN_TTL must be a duration of at least 1h, such as 168h")
	}
	if d.AnnounceInterval, err = time.ParseDuration(getEnv("SERVICE_ANNOUNCE_INTERVAL", servicebus.DefaultAnnounceInterval.String())); err != nil || d.AnnounceInterval < time.Second {
		return Discovery{}, errors.New("SERVICE_ANNOUNCE_INTERVAL must be a duration of at least 1s, such as 1m")
	}
	if d.Secret == "" {
		return d, nil
	}
	if len(d.Secret) < servicebus.MinSecretLen {
		return Discovery{}, fmt.Errorf("SERVICE_DISCOVERY_SECRET must be at least %d bytes", servicebus.MinSecretLen)
	}
	if u, err := url.Parse(d.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Discovery{}, errors.New("SERVICE_URL must be the http(s):// URL the other services reach this server at, e.g. http://units:8080")
	}
	return d, nil
}
