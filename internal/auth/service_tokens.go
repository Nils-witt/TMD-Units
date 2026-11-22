package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"go-unit-mangement/internal/models"
)

// ServiceUserPrefix starts the username of the account a service gets its
// tokens for over service discovery (see ReplaceServiceToken).
const ServiceUserPrefix = "service:"

// serviceTokenPrefix starts the name of a service token; the client's
// instance follows.
const serviceTokenPrefix = "auto:"

// ErrNotServiceUser means the service's username belongs to an account
// people sign in with, so no service token is issued for it.
var ErrNotServiceUser = errors.New("the service's account is a regular account")

// ReplaceServiceToken issues an API token valid for ttl to the instance
// instance of the service client, ending the one that instance had before.
// The token acts as the account "service:<client>", created on first use:
// not an administrator, with no password and no SSO identity, so nobody can
// sign in as it. Deleting the account ends its tokens; the service then asks
// for a new one, which creates it again.
func (s *Service) ReplaceServiceToken(ctx context.Context, client, instance string, ttl time.Duration) (string, *models.Session, error) {
	client, instance = strings.TrimSpace(client), strings.TrimSpace(instance)
	if client == "" {
		return "", nil, ErrInvalidUsername
	}
	username, err := normalizeUsername(ServiceUserPrefix + client)
	if err != nil {
		return "", nil, err
	}
	name := truncateRunes(serviceTokenPrefix+instance, MaxTokenNameLength)
	if ttl < MinTokenTTL || ttl > MaxTokenTTL {
		return "", nil, ErrInvalidTokenTTL
	}

	var created *newSession
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := ensureServiceUser(tx, username)
		if err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND api_token AND name = ?", user.ID, name).Delete(&models.Session{}).Error; err != nil {
			return fmt.Errorf("delete old service token: %w", err)
		}
		created, err = s.createSessionIn(ctx, tx, models.Session{UserID: user.ID, APIToken: true, Name: name}, ttl)
		if err != nil {
			return err
		}
		created.User = *user
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("replace token of %s: %w", username, err)
	}
	return created.token, &created.Session, nil
}

// ensureServiceUser returns the service account username, creating it if
// needed. Two instances asking at once both end up with the same account.
func ensureServiceUser(tx *gorm.DB, username string) (*models.User, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.User{Username: username}).Error; err != nil {
		return nil, fmt.Errorf("create service account: %w", err)
	}
	var user models.User
	if err := tx.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, fmt.Errorf("get service account: %w", err)
	}
	// An account someone made by hand under that name must not be handed
	// to whoever holds the discovery secret.
	if user.IsAdmin || user.HasPassword() || user.SSO() {
		return nil, ErrNotServiceUser
	}
	return &user, nil
}
