package units

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go-unit-mangement/internal/models"
)

const (
	// MaxAttributes caps how many attributes a unit may have.
	MaxAttributes = 50
	// MaxAttributeKeyLength caps an attribute key, in characters.
	MaxAttributeKeyLength = 64
	// MaxAttributeTextLength caps a text attribute's value, in characters.
	MaxAttributeTextLength = 1024
	// MaxAttributeURLLength caps a URL attribute's value, in bytes.
	MaxAttributeURLLength = 2048
)

// AttributeType is the type of a unit attribute's value.
type AttributeType string

const (
	// AttributeText is a string of at most MaxAttributeTextLength characters.
	AttributeText AttributeType = "text"
	// AttributeNumber is a finite JSON number.
	AttributeNumber AttributeType = "number"
	// AttributeBoolean is true or false.
	AttributeBoolean AttributeType = "boolean"
	// AttributeDate is a calendar date as YYYY-MM-DD.
	AttributeDate AttributeType = "date"
	// AttributeURL is an absolute http or https URL.
	AttributeURL AttributeType = "url"
)

// AttributeTypes lists the attribute types.
var AttributeTypes = []AttributeType{AttributeText, AttributeNumber, AttributeBoolean, AttributeDate, AttributeURL}

// ErrInvalidAttributes means a unit's attributes break one of the rules of
// normalizeAttributes; the wrapping error says which.
var ErrInvalidAttributes = errors.New("invalid unit attributes")

// attributeError wraps ErrInvalidAttributes with what is wrong with which
// attribute.
func attributeError(i int, key, format string, args ...any) error {
	return fmt.Errorf("%w: attribute %d (%q): %s", ErrInvalidAttributes, i+1, key, fmt.Sprintf(format, args...))
}

// normalizeAttributes validates in and returns a copy with trimmed keys and
// values re-encoded canonically, so equal values have equal bytes. It returns
// nil when in is empty and always a new slice otherwise, so the unit's old
// attributes stay intact for Changed.
func normalizeAttributes(in []models.UnitAttribute) ([]models.UnitAttribute, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > MaxAttributes {
		return nil, fmt.Errorf("%w: a unit can have at most %d attributes", ErrInvalidAttributes, MaxAttributes)
	}
	out := make([]models.UnitAttribute, len(in))
	seen := make(map[string]bool, len(in))
	for i, a := range in {
		key := strings.TrimSpace(a.Key)
		if key == "" || utf8.RuneCountInString(key) > MaxAttributeKeyLength || strings.ContainsFunc(key, unicode.IsControl) {
			return nil, attributeError(i, key, "the key must be 1 to %d characters without control characters", MaxAttributeKeyLength)
		}
		folded := strings.ToLower(key)
		if seen[folded] {
			return nil, attributeError(i, key, "the key is used more than once")
		}
		seen[folded] = true
		value, err := normalizeAttributeValue(AttributeType(a.Type), a.Value)
		if err != nil {
			return nil, attributeError(i, key, "%v", err)
		}
		out[i] = models.UnitAttribute{Key: key, Type: a.Type, Value: value}
	}
	return out, nil
}

// normalizeAttributeValue checks that raw is a value of typ and returns its
// canonical encoding.
func normalizeAttributeValue(typ AttributeType, raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, fmt.Errorf("the value is missing")
	}
	var v any
	switch typ {
	case AttributeText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("a text value must be a string")
		}
		if utf8.RuneCountInString(s) > MaxAttributeTextLength ||
			strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
			return nil, fmt.Errorf("a text value must be at most %d characters without control characters other than newlines and tabs", MaxAttributeTextLength)
		}
		v = s
	case AttributeNumber:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("a number value must be a finite number")
		}
		v = f
	case AttributeBoolean:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("a boolean value must be true or false")
		}
		v = b
	case AttributeDate:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("a date value must be a YYYY-MM-DD string")
		}
		s = strings.TrimSpace(s)
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return nil, fmt.Errorf("a date value must be a valid YYYY-MM-DD date")
		}
		v = s
	case AttributeURL:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("a url value must be a string")
		}
		s = strings.TrimSpace(s)
		u, err := url.Parse(s)
		if err != nil || len(s) > MaxAttributeURLLength || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("a url value must be an absolute http or https URL of at most %d bytes", MaxAttributeURLLength)
		}
		v = s
	default:
		return nil, fmt.Errorf("the type must be one of text, number, boolean, date or url")
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	return out, nil
}

// sameAttributes reports whether a and b hold the same attributes in the
// same order. Both must be normalized.
func sameAttributes(a, b []models.UnitAttribute) bool {
	return slices.EqualFunc(a, b, func(x, y models.UnitAttribute) bool {
		return x.Key == y.Key && x.Type == y.Type && bytes.Equal(x.Value, y.Value)
	})
}
