package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-unit-mangement/internal/audit"
	"go-unit-mangement/internal/models"
	"go-unit-mangement/internal/servicebus"
)

type fakeTokens struct {
	err          error
	client, inst string
	ttl          time.Duration
}

func (f *fakeTokens) ReplaceServiceToken(_ context.Context, client, instance string, ttl time.Duration) (string, *models.Session, error) {
	f.client, f.inst, f.ttl = client, instance, ttl
	if f.err != nil {
		return "", nil, f.err
	}
	return "tok", &models.Session{ID: 7, Name: "auto:" + instance, ExpiresAt: time.Unix(100, 0)}, nil
}

type fakeAudit struct{ entries []audit.Entry }

func (f *fakeAudit) Record(_ context.Context, e audit.Entry) { f.entries = append(f.entries, e) }

func TestTokenIssuer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		client    string
		storeErr  error
		wantToken string
		wantErr   error
	}{
		{"allowed", "api", nil, "tok", nil},
		{"not allowed", "tiles", nil, "", errClientNotAllowed},
		{"store fails", "api", errors.New("db down"), "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tokens, auditLog := &fakeTokens{err: tc.storeErr}, &fakeAudit{}
			issue := tokenIssuer(tokens, auditLog, []string{"api"}, 2*time.Hour)
			token, expires, err := issue(t.Context(), servicebus.TokenRequest{Client: tc.client, ClientInstance: "i1"})
			if token != tc.wantToken {
				t.Errorf("token = %q, want %q", token, tc.wantToken)
			}
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			case tc.storeErr != nil && (err == nil || errors.Is(err, tc.storeErr)):
				// The store's error stays in the log, not in the reply.
				t.Errorf("err = %v, want a generic error", err)
			case tc.wantToken != "":
				if err != nil || !expires.Equal(time.Unix(100, 0)) || tokens.inst != "i1" || tokens.ttl != 2*time.Hour {
					t.Errorf("err %v, expires %v, store saw %+v", err, expires, tokens)
				}
				if len(auditLog.entries) != 1 || auditLog.entries[0].Action != audit.ActionTokenCreate {
					t.Errorf("audit = %+v", auditLog.entries)
				}
			}
		})
	}
}
