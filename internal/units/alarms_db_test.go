package units

import (
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecordAlarm(t *testing.T) {
	s, user := newDBService(t)
	ctx := t.Context()

	unit, err := s.Create(ctx, Input{Name: rand.Text()[:8] + "-alarm"}, user)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, Input{Name: rand.Text()[:8] + "-alarm"}, user)
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := s.Subscribe()
	defer unsubscribe()

	t0 := time.Now().UTC().Truncate(time.Second)
	first, created, err := s.RecordAlarm(ctx, unit.ID, AlarmInput{Latitude: 50, Longitude: 7, Title: "Brand", ReferenceID: strPtr("E-1"), Timestamp: t0}, user)
	if err != nil || !created {
		t.Fatalf("first alarm: created = %v, err = %v", created, err)
	}
	if first.Unit == nil || first.Unit.Name != unit.Name || first.CreatedBy == nil || first.CreatedBy.ID != user.ID {
		t.Errorf("first alarm = %+v, want unit name and creator", first)
	}
	select {
	case ev := <-events:
		if ev.Type != EventAlarm || ev.ID != unit.ID || ev.Alarm == nil || ev.Alarm.ID != first.ID {
			t.Errorf("event = %+v, want alarm event for the unit", ev)
		}
	default:
		t.Error("alarm published no event")
	}

	updated, created, err := s.RecordAlarm(ctx, unit.ID, AlarmInput{Latitude: 51, Longitude: 8, Title: "Brand 2", ReferenceID: strPtr("E-1"), Details: strPtr("mehr"), Timestamp: t0.Add(time.Minute)}, user)
	if err != nil || created {
		t.Fatalf("resent alarm: created = %v, err = %v; want update", created, err)
	}
	if updated.ID != first.ID || updated.Title != "Brand 2" || updated.Latitude != 51 || updated.Details == nil || !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("updated alarm = %+v, want the first one changed", updated)
	}

	// The same reference ID on another unit, and alarms without one, are new.
	if _, created, err := s.RecordAlarm(ctx, other.ID, AlarmInput{Latitude: 50, Longitude: 7, Title: "Brand", ReferenceID: strPtr("E-1")}, user); err != nil || !created {
		t.Errorf("other unit: created = %v, err = %v; want new", created, err)
	}
	for range 2 {
		if _, created, err := s.RecordAlarm(ctx, unit.ID, AlarmInput{Latitude: 50, Longitude: 7, Title: "ohne Referenz", Timestamp: t0.Add(-time.Hour)}, user); err != nil || !created {
			t.Errorf("without reference: created = %v, err = %v; want new", created, err)
		}
	}

	alarms, err := s.Alarms(ctx, unit.ID, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(alarms) != 3 || alarms[0].ID != first.ID {
		t.Fatalf("got %d alarms (first %v), want 3 with the updated one first", len(alarms), alarms[0].ID)
	}
	if recent, err := s.Alarms(ctx, unit.ID, 0, t0, time.Time{}); err != nil || len(recent) != 1 {
		t.Errorf("since t0: got %d alarms, err = %v; want 1", len(recent), err)
	}

	if _, _, err := s.RecordAlarm(ctx, uuid.New(), AlarmInput{Latitude: 50, Longitude: 7, Title: "x"}, user); !errors.Is(err, ErrUnitNotFound) {
		t.Errorf("unknown unit: err = %v, want ErrUnitNotFound", err)
	}
	if _, err := s.Alarms(ctx, uuid.New(), 0, time.Time{}, time.Time{}); !errors.Is(err, ErrUnitNotFound) {
		t.Errorf("unknown unit list: err = %v, want ErrUnitNotFound", err)
	}

	if _, err := s.Delete(ctx, unit.ID); err != nil {
		t.Fatal(err)
	}
	var left int64
	if err := s.db.Table("unit_alarms").Where("unit_id = ?", unit.ID).Count(&left).Error; err != nil || left != 0 {
		t.Errorf("after delete: %d alarms left, err = %v; want 0", left, err)
	}
}
