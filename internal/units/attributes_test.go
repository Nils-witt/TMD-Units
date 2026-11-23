package units

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go-unit-mangement/internal/models"
)

func attr(key, typ, value string) models.UnitAttribute {
	return models.UnitAttribute{Key: key, Type: typ, Value: json.RawMessage(value)}
}

func TestNormalizeAttributes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []models.UnitAttribute
		want    []models.UnitAttribute
		wantErr bool
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []models.UnitAttribute{}, want: nil},
		{
			name: "every type, canonical and in order",
			in: []models.UnitAttribute{
				attr(" Kennzeichen ", "text", `"B-RK 1234"`),
				attr("Sitzplätze", "number", `4.0`),
				attr("Allrad", "boolean", ` true `),
				attr("TÜV bis", "date", `" 2027-03-31 "`),
				attr("Handbuch", "url", `"https://example.com/a?b=c"`),
			},
			want: []models.UnitAttribute{
				attr("Kennzeichen", "text", `"B-RK 1234"`),
				attr("Sitzplätze", "number", `4`),
				attr("Allrad", "boolean", `true`),
				attr("TÜV bis", "date", `"2027-03-31"`),
				attr("Handbuch", "url", `"https://example.com/a?b=c"`),
			},
		},
		{name: "multiline text", in: []models.UnitAttribute{attr("a", "text", `"x\n\ty"`)}, want: []models.UnitAttribute{attr("a", "text", `"x\n\ty"`)}},
		{name: "empty key", in: []models.UnitAttribute{attr(" ", "text", `"x"`)}, wantErr: true},
		{name: "long key", in: []models.UnitAttribute{attr(strings.Repeat("k", MaxAttributeKeyLength+1), "text", `"x"`)}, wantErr: true},
		{name: "control in key", in: []models.UnitAttribute{attr("a\x00", "text", `"x"`)}, wantErr: true},
		{name: "duplicate key ignoring case", in: []models.UnitAttribute{attr("Farbe", "text", `"rot"`), attr("farbe ", "text", `"blau"`)}, wantErr: true},
		{name: "unknown type", in: []models.UnitAttribute{attr("a", "color", `"red"`)}, wantErr: true},
		{name: "missing value", in: []models.UnitAttribute{{Key: "a", Type: "text"}}, wantErr: true},
		{name: "null value", in: []models.UnitAttribute{attr("a", "boolean", `null`)}, wantErr: true},
		{name: "text not a string", in: []models.UnitAttribute{attr("a", "text", `5`)}, wantErr: true},
		{name: "long text", in: []models.UnitAttribute{attr("a", "text", `"`+strings.Repeat("x", MaxAttributeTextLength+1)+`"`)}, wantErr: true},
		{name: "control in text", in: []models.UnitAttribute{attr("a", "text", `"x\u0007"`)}, wantErr: true},
		{name: "number as string", in: []models.UnitAttribute{attr("a", "number", `"4"`)}, wantErr: true},
		{name: "boolean as string", in: []models.UnitAttribute{attr("a", "boolean", `"true"`)}, wantErr: true},
		{name: "invalid date", in: []models.UnitAttribute{attr("a", "date", `"2027-02-30"`)}, wantErr: true},
		{name: "date with time", in: []models.UnitAttribute{attr("a", "date", `"2027-02-03T10:00:00Z"`)}, wantErr: true},
		{name: "relative url", in: []models.UnitAttribute{attr("a", "url", `"/docs"`)}, wantErr: true},
		{name: "javascript url", in: []models.UnitAttribute{attr("a", "url", `"javascript:alert(1)"`)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeAttributes(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidAttributes) {
					t.Fatalf("err = %v, want ErrInvalidAttributes", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tt.want == nil) || !sameAttributes(got, tt.want) {
				t.Errorf("got %s, want %s", attrsString(got), attrsString(tt.want))
			}
		})
	}
}

func TestNormalizeAttributesLimit(t *testing.T) {
	t.Parallel()
	in := make([]models.UnitAttribute, MaxAttributes+1)
	for i := range in {
		in[i] = attr(fmt.Sprintf("k%d", i), "boolean", "true")
	}
	if _, err := normalizeAttributes(in[:MaxAttributes]); err != nil {
		t.Errorf("%d attributes: %v", MaxAttributes, err)
	}
	if _, err := normalizeAttributes(in); !errors.Is(err, ErrInvalidAttributes) {
		t.Errorf("%d attributes: err = %v, want ErrInvalidAttributes", MaxAttributes+1, err)
	}
}

func TestNormalizeAttributesCopies(t *testing.T) {
	t.Parallel()
	in := []models.UnitAttribute{attr(" a ", "number", "1.50")}
	if _, err := normalizeAttributes(in); err != nil {
		t.Fatal(err)
	}
	if in[0].Key != " a " || string(in[0].Value) != "1.50" {
		t.Errorf("input changed to %+v", in[0])
	}
}

func TestChangedAttributes(t *testing.T) {
	t.Parallel()
	a := models.Unit{Name: "A", Attributes: []models.UnitAttribute{attr("x", "number", "4")}}
	b := a
	if err := apply(&b, Input{Name: "A", Attributes: []models.UnitAttribute{attr("x", "number", "4.0")}}); err != nil {
		t.Fatal(err)
	}
	if got := Changed(&a, &b); len(got) != 0 {
		t.Errorf("same value: changed = %v, want none", got)
	}
	if err := apply(&b, Input{Name: "A", Attributes: []models.UnitAttribute{attr("x", "number", "5")}}); err != nil {
		t.Fatal(err)
	}
	if got := Changed(&a, &b); !slices.Equal(got, []Field{FieldAttributes}) {
		t.Errorf("new value: changed = %v, want [attributes]", got)
	}
	if err := apply(&b, Input{Name: "A"}); err != nil {
		t.Fatal(err)
	}
	if b.Attributes != nil {
		t.Errorf("cleared attributes = %v, want nil", b.Attributes)
	}
	if got := Changed(&a, &b); !slices.Equal(got, []Field{FieldAttributes}) {
		t.Errorf("cleared: changed = %v, want [attributes]", got)
	}
}

func attrsString(attrs []models.UnitAttribute) string {
	b, _ := json.Marshal(attrs)
	return string(b)
}
