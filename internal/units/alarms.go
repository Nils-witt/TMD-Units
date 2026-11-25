package units

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"go-unit-mangement/internal/models"
)

const (
	MaxAlarmTitleLength       = 200
	MaxAlarmAddressLength     = 500
	MaxAlarmDetailsLength     = 10000
	MaxAlarmReferenceIDLength = 128
)

// ErrInvalidAlarm means an alarm breaks one of the rules of AlarmInput.
var ErrInvalidAlarm = fmt.Errorf("alarms need a latitude between -90 and 90, a longitude between -180 and 180 and a title of 1 to %d characters; the address may have up to %d characters, the reference ID up to %d and the details up to %d, all without control characters (the details may have line breaks and tabs)",
	MaxAlarmTitleLength, MaxAlarmAddressLength, MaxAlarmReferenceIDLength, MaxAlarmDetailsLength)

// AlarmInput is an alarm as sent to a unit. Address, Details and ReferenceID
// are optional; blank ones count as missing.
type AlarmInput struct {
	Latitude    float64
	Longitude   float64
	Title       string
	Address     *string
	Details     *string
	ReferenceID *string
	// Timestamp is when the alarm was raised.
	Timestamp time.Time
}

// RecordAlarm stores an alarm for the unit and reports whether it is new. An
// alarm with the reference ID of one the unit already has replaces that one
// instead (created is then false). Alarms can be sent to mirrored units too:
// they stay on this instance. The returned alarm's Unit has only its ID and
// name.
func (s *Service) RecordAlarm(ctx context.Context, unitID uuid.UUID, in AlarmInput, by *models.User) (*models.UnitAlarm, bool, error) {
	if err := normalizeAlarm(&in); err != nil {
		return nil, false, err
	}
	var alarm models.UnitAlarm
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Locking the unit serializes alarms per unit, so two alarms with
		// the same reference ID can't both be inserted.
		var unit models.Unit
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "name").First(&unit, "id = ?", unitID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUnitNotFound
		}
		if err != nil {
			return fmt.Errorf("get unit %s: %w", unitID, err)
		}
		if in.ReferenceID != nil {
			err := tx.First(&alarm, "unit_id = ? AND reference_id = ?", unitID, *in.ReferenceID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("get alarm of unit %s: %w", unitID, err)
			}
		}
		if alarm.ID == uuid.Nil {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("generate alarm id: %w", err)
			}
			alarm = models.UnitAlarm{ID: id, UnitID: unitID, CreatedByID: &by.ID}
			created = true
		}
		alarm.Latitude, alarm.Longitude = in.Latitude, in.Longitude
		alarm.Title, alarm.Address, alarm.Details, alarm.ReferenceID = in.Title, in.Address, in.Details, in.ReferenceID
		alarm.Timestamp = in.Timestamp
		alarm.UpdatedByID = &by.ID
		if err := tx.Save(&alarm).Error; err != nil {
			return fmt.Errorf("save alarm of unit %s: %w", unitID, err)
		}
		if err := tx.Preload("CreatedBy").Preload("UpdatedBy").First(&alarm, "id = ?", alarm.ID).Error; err != nil {
			return fmt.Errorf("get alarm %s: %w", alarm.ID, err)
		}
		alarm.Unit = &unit
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	s.events.Publish(Event{Type: EventAlarm, ID: unitID, Alarm: &alarm})
	return &alarm, created, nil
}

// Alarms returns up to limit of the unit's alarms, newest first. A non-zero
// since leaves out alarms raised before it, a non-zero to those raised after
// it.
func (s *Service) Alarms(ctx context.Context, unitID uuid.UUID, limit int, since, to time.Time) ([]models.UnitAlarm, error) {
	if limit <= 0 || limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.Unit{}).Where("id = ?", unitID).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("get unit %s: %w", unitID, err)
	}
	if count == 0 {
		return nil, ErrUnitNotFound
	}
	query := s.db.WithContext(ctx).Preload("CreatedBy").Preload("UpdatedBy").Where("unit_id = ?", unitID)
	if !since.IsZero() {
		query = query.Where("timestamp >= ?", since)
	}
	if !to.IsZero() {
		query = query.Where("timestamp <= ?", to)
	}
	var alarms []models.UnitAlarm
	if err := query.Order("timestamp DESC").Order("id DESC").Limit(limit).Find(&alarms).Error; err != nil {
		return nil, fmt.Errorf("get alarms of unit %s: %w", unitID, err)
	}
	return alarms, nil
}

func normalizeAlarm(in *AlarmInput) error {
	if !validCoordinate(in.Latitude, 90) || !validCoordinate(in.Longitude, 180) {
		return ErrInvalidAlarm
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || !validAlarmText(in.Title, MaxAlarmTitleLength, false) {
		return ErrInvalidAlarm
	}
	for _, f := range []struct {
		v         **string
		max       int
		multiline bool
	}{
		{&in.Address, MaxAlarmAddressLength, false},
		{&in.Details, MaxAlarmDetailsLength, true},
		{&in.ReferenceID, MaxAlarmReferenceIDLength, false},
	} {
		if *f.v == nil {
			continue
		}
		v := strings.TrimSpace(**f.v)
		if v == "" {
			*f.v = nil
			continue
		}
		if !validAlarmText(v, f.max, f.multiline) {
			return ErrInvalidAlarm
		}
		*f.v = &v
	}
	if in.Timestamp.IsZero() {
		in.Timestamp = time.Now()
	}
	in.Timestamp = in.Timestamp.UTC()
	return nil
}

// validAlarmText reports whether v is valid UTF-8 of at most limit
// characters without control characters, except line breaks and tabs when
// multiline.
func validAlarmText(v string, limit int, multiline bool) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > limit {
		return false
	}
	for _, r := range v {
		allowed := multiline && (r == '\n' || r == '\r' || r == '\t')
		if unicode.IsControl(r) && !allowed {
			return false
		}
	}
	return true
}
