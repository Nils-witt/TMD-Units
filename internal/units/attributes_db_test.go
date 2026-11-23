package units

import (
	"crypto/rand"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"go-unit-mangement/internal/models"
)

func TestAttributesRoundTrip(t *testing.T) {
	s, user := newDBService(t)
	ctx := t.Context()

	attrs := []models.UnitAttribute{attr("Kennzeichen", "text", `"B-RK 1234"`), attr("Sitze", "number", `4.0`), attr("Allrad", "boolean", `true`)}
	unit, err := s.Create(ctx, Input{Name: rand.Text()[:8] + "-attrs", Attributes: attrs}, user)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []models.UnitAttribute{attr("Kennzeichen", "text", `"B-RK 1234"`), attr("Sitze", "number", `4`), attr("Allrad", "boolean", `true`)}
	if !sameAttributes(got.Attributes, want) {
		t.Errorf("stored attributes = %s, want %s", attrsString(got.Attributes), attrsString(want))
	}

	// A patch of another field keeps the attributes and changes only that.
	_, changed, err := s.Patch(ctx, unit.ID, func(in *Input) { in.Name += "-renamed" }, user)
	if err != nil || !slices.Equal(changed, []Field{FieldName}) {
		t.Fatalf("rename: changed = %v, err = %v; want [name]", changed, err)
	}
	// Setting equal values again changes nothing.
	_, changed, err = s.Patch(ctx, unit.ID, func(in *Input) { in.Attributes = attrs }, user)
	if err != nil || len(changed) != 0 {
		t.Fatalf("same attributes: changed = %v, err = %v; want none", changed, err)
	}
	updated, changed, err := s.Patch(ctx, unit.ID, func(in *Input) { in.Attributes = in.Attributes[1:] }, user)
	if err != nil || !slices.Equal(changed, []Field{FieldAttributes}) {
		t.Fatalf("drop one: changed = %v, err = %v; want [attributes]", changed, err)
	}
	if !sameAttributes(updated.Attributes, want[1:]) {
		t.Errorf("attributes = %s, want %s", attrsString(updated.Attributes), attrsString(want[1:]))
	}
	_, _, err = s.Patch(ctx, unit.ID, func(in *Input) { in.Attributes = []models.UnitAttribute{attr("x", "date", `"soon"`)} }, user)
	if !errors.Is(err, ErrInvalidAttributes) {
		t.Errorf("invalid: err = %v, want ErrInvalidAttributes", err)
	}
	cleared, _, err := s.Patch(ctx, unit.ID, func(in *Input) { in.Attributes = nil }, user)
	if err != nil || cleared.Attributes != nil {
		t.Errorf("clear: attributes = %v, err = %v; want nil", cleared.Attributes, err)
	}
}

func TestUpsertSyncedAttributes(t *testing.T) {
	s, _ := newDBService(t)
	ctx := t.Context()

	remote := models.SyncRemote{ID: uuid.New(), Name: "remote", BaseURL: "https://remote.example", PollIntervalSec: 60, Enabled: true}
	if err := s.db.Create(&remote).Error; err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	in := Input{Name: rand.Text()[:8] + "-synced-attrs", Attributes: []models.UnitAttribute{attr("a", "date", `"2027-01-01"`)}}
	if changed, err := s.UpsertSynced(ctx, remote.ID, id, in); err != nil || !changed {
		t.Fatalf("create: changed = %v, err = %v", changed, err)
	}
	if changed, err := s.UpsertSynced(ctx, remote.ID, id, in); err != nil || changed {
		t.Fatalf("unchanged: changed = %v, err = %v", changed, err)
	}
	in.Attributes = nil
	if changed, err := s.UpsertSynced(ctx, remote.ID, id, in); err != nil || !changed {
		t.Fatalf("clear: changed = %v, err = %v", changed, err)
	}
	unit, err := s.Get(ctx, id)
	if err != nil || unit.Attributes != nil {
		t.Errorf("attributes = %v, err = %v; want nil", unit.Attributes, err)
	}
}
