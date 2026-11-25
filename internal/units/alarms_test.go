package units

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestNormalizeAlarm(t *testing.T) {
	valid := func() AlarmInput {
		return AlarmInput{Latitude: 50.7, Longitude: 7.1, Title: " Brand "}
	}
	in := valid()
	in.Address, in.Details, in.ReferenceID = strPtr("  "), strPtr("Zeile 1\nZeile 2\t!"), strPtr(" E-1 ")
	if err := normalizeAlarm(&in); err != nil {
		t.Fatal(err)
	}
	if in.Title != "Brand" || in.Address != nil || *in.Details != "Zeile 1\nZeile 2\t!" || *in.ReferenceID != "E-1" {
		t.Errorf("got %+v, want trimmed title and reference ID, blank address dropped, details kept", in)
	}
	if in.Timestamp.IsZero() || in.Timestamp.Location() != time.UTC {
		t.Errorf("timestamp = %v, want now in UTC", in.Timestamp)
	}

	tests := []struct {
		name   string
		modify func(*AlarmInput)
	}{
		{"latitude out of range", func(in *AlarmInput) { in.Latitude = 91 }},
		{"longitude NaN", func(in *AlarmInput) { in.Longitude = math.NaN() }},
		{"blank title", func(in *AlarmInput) { in.Title = "  " }},
		{"title too long", func(in *AlarmInput) { in.Title = strings.Repeat("ä", MaxAlarmTitleLength+1) }},
		{"line break in title", func(in *AlarmInput) { in.Title = "a\nb" }},
		{"line break in address", func(in *AlarmInput) { in.Address = strPtr("a\nb") }},
		{"control character in details", func(in *AlarmInput) { in.Details = strPtr("a\x00b") }},
		{"reference ID too long", func(in *AlarmInput) { in.ReferenceID = strPtr(strings.Repeat("x", MaxAlarmReferenceIDLength+1)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := valid()
			tt.modify(&in)
			if err := normalizeAlarm(&in); !errors.Is(err, ErrInvalidAlarm) {
				t.Errorf("err = %v, want ErrInvalidAlarm", err)
			}
		})
	}
}
