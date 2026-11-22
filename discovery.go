package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"go-unit-mangement/internal/audit"
	"go-unit-mangement/internal/config"
	"go-unit-mangement/internal/models"
	"go-unit-mangement/internal/servicebus"
	"go-unit-mangement/internal/version"
)

// discoveryServiceName is what this server announces itself as.
const discoveryServiceName = "units"

// errClientNotAllowed refuses a token request from a service not listed in
// SERVICE_TOKEN_CLIENTS.
var errClientNotAllowed = errors.New("this service may not get a token here")

// serviceTokenStore issues service tokens; *auth.Service implements it.
type serviceTokenStore interface {
	ReplaceServiceToken(ctx context.Context, client, instance string, ttl time.Duration) (string, *models.Session, error)
}

// auditRecorder records issued tokens; *audit.Service implements it.
type auditRecorder interface {
	Record(ctx context.Context, e audit.Entry)
}

// startDiscovery announces this server on the broker and answers the token
// requests of the allowed services, until ctx is done. It does nothing
// without SERVICE_DISCOVERY_SECRET or AMQP_URL.
func startDiscovery(ctx context.Context, cfg config.Discovery, tokens serviceTokenStore, auditLog auditRecorder, instanceName string) error {
	if cfg.Secret == "" {
		return nil
	}
	if cfg.AMQPURL == "" {
		slog.Warn("SERVICE_DISCOVERY_SECRET is set but AMQP_URL is not; service discovery is off")
		return nil
	}

	host, _ := os.Hostname()
	name := "units " + host
	if instanceName != "" {
		name = instanceName + " (" + host + ")"
	}
	bus, err := servicebus.New(servicebus.Options{
		URL:    cfg.AMQPURL,
		Secret: []byte(cfg.Secret),
		Name:   "units discovery " + host,
		Self: servicebus.Announcement{
			Service: discoveryServiceName, InstanceID: host, Name: name,
			URL: cfg.URL, Version: version.Version,
		},
		AnnounceInterval: cfg.AnnounceInterval,
	})
	if err != nil {
		return fmt.Errorf("service discovery: %w", err)
	}
	bus.ServeTokens(tokenIssuer(tokens, auditLog, cfg.Clients, cfg.TokenTTL))
	go bus.Run(ctx)

	slog.Info("service discovery on", "service_url", cfg.URL, "token_clients", strings.Join(cfg.Clients, ","))
	return nil
}

// tokenIssuer issues a token valid for ttl to the services in clients.
func tokenIssuer(tokens serviceTokenStore, auditLog auditRecorder, clients []string, ttl time.Duration) servicebus.IssueFunc {
	return func(ctx context.Context, req servicebus.TokenRequest) (string, time.Time, error) {
		if !slices.Contains(clients, req.Client) {
			return "", time.Time{}, fmt.Errorf("%w: %q", errClientNotAllowed, req.Client)
		}
		token, session, err := tokens.ReplaceServiceToken(ctx, req.Client, req.ClientInstance, ttl)
		if err != nil {
			slog.Error("service discovery: issuing a token failed", "client", req.Client, "err", err)
			return "", time.Time{}, errors.New("issuing the token failed")
		}
		slog.Info("service discovery: issued a token", "client", req.Client, "instance", req.ClientInstance, "token_id", session.ID)
		auditLog.Record(ctx, audit.Entry{
			Action: audit.ActionTokenCreate, ActorName: "service discovery",
			TargetType: audit.TargetToken, TargetID: fmt.Sprint(session.ID), TargetName: session.Name,
			Details: map[string]any{"user": session.User.Username},
		})
		return token, session.ExpiresAt, nil
	}
}
