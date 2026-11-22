package auth

import (
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go-unit-mangement/internal/database"
)

// TestReplaceServiceToken needs a scratch PostgreSQL database in
// TEST_DATABASE_URL (see TestSyncOIDCGroups).
func TestReplaceServiceToken(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Connect(dsn)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(db, time.Hour, []byte(strings.Repeat("k", MinJWTSecretLength)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	client := "test-" + rand.Text()[:8]

	first, session, err := s.ReplaceServiceToken(ctx, client, "i1", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if session.Name != "auto:i1" || !session.APIToken {
		t.Fatalf("session = name %q, api token %t", session.Name, session.APIToken)
	}
	user, err := s.UserForToken(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != ServiceUserPrefix+client || user.IsAdmin || user.HasPassword() || user.SSO() {
		t.Fatalf("token acts as %+v, want a plain service account", user)
	}

	other, _, err := s.ReplaceServiceToken(ctx, client, "i2", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.ReplaceServiceToken(ctx, client, "i1", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserForToken(ctx, first); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("replaced token: err = %v, want ErrInvalidSession", err)
	}
	for name, token := range map[string]string{"new token": second, "other instance's token": other} {
		if u, err := s.UserForToken(ctx, token); err != nil || u.ID != user.ID {
			t.Errorf("%s: user %v, err %v; want the service account", name, u, err)
		}
	}

	// A regular account under a service's name is never handed out.
	taken := "test-" + rand.Text()[:8]
	if _, err := s.CreateUser(ctx, ServiceUserPrefix+taken, "password123", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReplaceServiceToken(ctx, taken, "i1", 24*time.Hour); !errors.Is(err, ErrNotServiceUser) {
		t.Errorf("regular account: err = %v, want ErrNotServiceUser", err)
	}
	if _, _, err := s.ReplaceServiceToken(ctx, client, "i1", time.Second); !errors.Is(err, ErrInvalidTokenTTL) {
		t.Errorf("short ttl: err = %v, want ErrInvalidTokenTTL", err)
	}
}
