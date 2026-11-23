package units

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go-unit-mangement/internal/models"
)

func TestCSVRoundTrip(t *testing.T) {
	id := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	list := []models.Unit{
		{
			ID: id, Name: `Alpha, "1"`,
			TacticalName: &models.TacticalName{Organisation: "Rotkreuz", Number: "83-1"},
			Symbol:       &models.UnitSymbol{Grundzeichen: "fahrzeug"},
			Groups:       []models.Group{{Name: "a"}, {Name: "b"}},
		},
		{ID: uuid.New(), Name: "Bravo"},
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, list, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte(utf8BOM)) {
		t.Error("export lacks the UTF-8 BOM")
	}
	rows, err := ParseCSV(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	r := rows[0]
	if r.ID != id || r.Name != list[0].Name || r.Line != 2 {
		t.Errorf("row = %+v, want id, name and line 2", r)
	}
	if r.TacticalName == nil || *r.TacticalName == nil || **r.TacticalName != *list[0].TacticalName {
		t.Errorf("tactical name = %v, want %v", r.TacticalName, list[0].TacticalName)
	}
	if r.Symbol == nil || *r.Symbol == nil || (*r.Symbol).Grundzeichen != "fahrzeug" {
		t.Errorf("symbol = %v, want grundzeichen fahrzeug", r.Symbol)
	}
	if !slices.Equal(r.Groups, []string{"a", "b"}) {
		t.Errorf("groups = %v, want [a b]", r.Groups)
	}
	if g := rows[1].Groups; g == nil || len(g) != 0 {
		t.Errorf("groups of unit without groups = %#v, want empty non-nil", g)
	}
}

func TestWriteCSVWithoutGroups(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCSV(&buf, []models.Unit{{ID: uuid.New(), Name: "A", Groups: []models.Group{{Name: "g"}}}}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), ColumnGroups) || strings.Contains(buf.String(), ",g") {
		t.Errorf("export without groups = %q, want no groups column", buf.String())
	}
}

func TestParseCSV(t *testing.T) {
	t.Run("semicolons and partial columns", func(t *testing.T) {
		rows, err := ParseCSV(strings.NewReader("Name;tactical_number\n Alpha ;1\n;\nBravo;\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Name != "Alpha" || rows[1].Name != "Bravo" || rows[1].Line != 4 {
			t.Fatalf("rows = %+v, want Alpha and Bravo (line 4), blank row skipped", rows)
		}
		if rows[0].Symbol != nil || rows[0].Groups != nil {
			t.Errorf("missing columns parsed as %+v, want nil", rows[0])
		}
		if tn := *rows[0].TacticalName; tn == nil || tn.Number != "1" {
			t.Errorf("tactical name = %v, want number 1", tn)
		}
	})
	t.Run("groups trimmed and deduplicated", func(t *testing.T) {
		rows, err := ParseCSV(strings.NewReader("name,groups\nA, x | y|x ||\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(rows[0].Groups, []string{"x", "y"}) {
			t.Errorf("groups = %v, want [x y]", rows[0].Groups)
		}
	})
	for name, input := range map[string]string{
		"empty":          "",
		"no name column": "id\n" + uuid.NewString() + "\n",
		"unknown column": "name,colour\nA,red\n",
		"duplicate":      "name,Name\nA,B\n",
		"invalid id":     "id,name\nnope,A\n",
		"too many cells": "name\nA,B\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCSV(strings.NewReader(input)); !errors.Is(err, ErrInvalidCSV) {
				t.Errorf("err = %v, want ErrInvalidCSV", err)
			}
		})
	}
}

func TestCSVAttributes(t *testing.T) {
	attrs := []models.UnitAttribute{attr("Kennzeichen", "text", `"B-RK 1, \"x\""`), attr("Sitze", "number", `4`)}
	list := []models.Unit{{ID: uuid.New(), Name: "A", Attributes: attrs}, {ID: uuid.New(), Name: "B"}}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, list, false); err != nil {
		t.Fatal(err)
	}
	rows, err := ParseCSV(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Attributes == nil || !sameAttributes(*rows[0].Attributes, attrs) {
		t.Errorf("attributes = %v, want %s", rows[0].Attributes, attrsString(attrs))
	}
	if a := rows[1].Attributes; a == nil || len(*a) != 0 {
		t.Errorf("attributes of unit without any = %v, want empty non-nil", a)
	}

	rows, err = ParseCSV(strings.NewReader("name\nA\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Attributes != nil {
		t.Errorf("attributes without the column = %v, want nil", rows[0].Attributes)
	}

	_, err = ParseCSV(strings.NewReader("name;attributes\nA;{not json\n"))
	if !errors.Is(err, ErrInvalidCSV) || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("invalid attributes: err = %v, want ErrInvalidCSV naming line 2", err)
	}
}
