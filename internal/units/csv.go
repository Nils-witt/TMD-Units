package units

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"go-unit-mangement/internal/models"
)

// MaxImportRows caps how many units one CSV import may contain.
const MaxImportRows = 10000

// GroupSeparator separates the group names in the groups column. It is
// neither of the column delimiters ParseCSV accepts.
const GroupSeparator = "|"

// CSV columns. Only ColumnName is required on import; see ParseCSV.
const (
	ColumnID   = "id"
	ColumnName = "name"
	// ColumnGroups holds the names of the unit's groups, joined by
	// GroupSeparator.
	ColumnGroups = "groups"
	// ColumnAttributes holds the unit's attributes as a JSON array, as in the
	// API; it is empty when the unit has none.
	ColumnAttributes = "attributes"
)

// tacticalNameColumns and symbolColumns list the columns of the parts of a
// unit's tactical name and symbol, in the order of their Fields.
var (
	tacticalNameColumns = []string{
		"tactical_organisation", "tactical_regional_association", "tactical_local_association",
		"tactical_function", "tactical_number",
	}
	symbolColumns = []string{
		"symbol_grundzeichen", "symbol_organisation", "symbol_fachaufgabe", "symbol_einheit",
		"symbol_verwaltungsstufe", "symbol_funktion", "symbol_symbol",
	}
)

// ErrInvalidCSV means an import file is not a valid unit CSV.
var ErrInvalidCSV = errors.New("invalid CSV")

// utf8BOM is written in front of exports so spreadsheet programs detect UTF-8.
const utf8BOM = "\xef\xbb\xbf"

// csvColumns returns the export columns, with the groups column only when
// withGroups is set.
func csvColumns(withGroups bool) []string {
	cols := []string{ColumnID, ColumnName}
	cols = append(cols, tacticalNameColumns...)
	cols = append(cols, symbolColumns...)
	cols = append(cols, ColumnAttributes)
	if withGroups {
		cols = append(cols, ColumnGroups)
	}
	return cols
}

// WriteCSV writes units as CSV with a header row. The groups column is only
// written when withGroups is set, since only administrators may assign
// groups and so import it.
func WriteCSV(w io.Writer, list []models.Unit, withGroups bool) error {
	if _, err := io.WriteString(w, utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(csvColumns(withGroups)); err != nil {
		return err
	}
	for i := range list {
		u := &list[i]
		record := []string{u.ID.String(), u.Name}
		tn := u.TacticalName
		if tn == nil {
			tn = &models.TacticalName{}
		}
		for _, f := range tn.Fields() {
			record = append(record, *f)
		}
		sym := u.Symbol
		if sym == nil {
			sym = &models.UnitSymbol{}
		}
		for _, f := range sym.Fields() {
			record = append(record, *f)
		}
		attrs := ""
		if len(u.Attributes) > 0 {
			b, err := json.Marshal(u.Attributes)
			if err != nil {
				return fmt.Errorf("encode attributes of unit %s: %w", u.ID, err)
			}
			attrs = string(b)
		}
		record = append(record, attrs)
		if withGroups {
			names := make([]string, len(u.Groups))
			for j, g := range u.Groups {
				names[j] = g.Name
			}
			record = append(record, strings.Join(names, GroupSeparator))
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ImportRow is one unit of a CSV import. Fields whose column the file lacks
// are nil: an update leaves them unchanged and a create leaves them empty.
type ImportRow struct {
	// Line is the row's line in the file, for error messages.
	Line int
	// ID is uuid.Nil when the row has none.
	ID   uuid.UUID
	Name string
	// TacticalName and Symbol point to nil to clear them.
	TacticalName **models.TacticalName
	Symbol       **models.UnitSymbol
	// Attributes points to the unit's attributes; an empty list clears them.
	Attributes *[]models.UnitAttribute
	// Groups are group names; a non-nil empty slice removes all groups.
	Groups []string
}

// ParseCSV reads a unit CSV as written by WriteCSV. The delimiter is a comma
// or, as spreadsheet programs in many locales write it, a semicolon; a
// leading UTF-8 BOM is skipped. The header row names the columns in any
// order. Only the name column is required; unknown columns are an error so
// typos don't go unnoticed. Rows without any value are skipped.
func ParseCSV(r io.Reader) ([]ImportRow, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte(utf8BOM))
	firstLine, _, _ := bytes.Cut(data, []byte{'\n'})
	cr := csv.NewReader(bytes.NewReader(data))
	if bytes.Count(firstLine, []byte{';'}) > bytes.Count(firstLine, []byte{','}) {
		cr.Comma = ';'
	}
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the file is empty", ErrInvalidCSV)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCSV, err)
	}
	index := map[string]int{}
	known := csvColumns(true)
	for i, col := range header {
		col = strings.ToLower(strings.TrimSpace(col))
		if !slices.Contains(known, col) {
			return nil, fmt.Errorf("%w: unknown column %q", ErrInvalidCSV, col)
		}
		if _, dup := index[col]; dup {
			return nil, fmt.Errorf("%w: duplicate column %q", ErrInvalidCSV, col)
		}
		index[col] = i
	}
	if _, ok := index[ColumnName]; !ok {
		return nil, fmt.Errorf("%w: the %q column is required", ErrInvalidCSV, ColumnName)
	}
	hasAny := func(cols []string) bool {
		return slices.ContainsFunc(cols, func(c string) bool { _, ok := index[c]; return ok })
	}
	hasTacticalName, hasSymbol := hasAny(tacticalNameColumns), hasAny(symbolColumns)
	_, hasGroups := index[ColumnGroups]
	_, hasAttributes := index[ColumnAttributes]

	var rows []ImportRow
	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCSV, err)
		}
		line, _ := cr.FieldPos(0)
		if !slices.ContainsFunc(record, func(v string) bool { return strings.TrimSpace(v) != "" }) {
			continue
		}
		if len(record) > len(header) {
			return nil, fmt.Errorf("%w: line %d has more fields than the header", ErrInvalidCSV, line)
		}
		if len(rows) == MaxImportRows {
			return nil, fmt.Errorf("%w: at most %d units can be imported at once", ErrInvalidCSV, MaxImportRows)
		}
		get := func(col string) string {
			i, ok := index[col]
			if !ok || i >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[i])
		}
		row := ImportRow{Line: line, Name: get(ColumnName)}
		if v := get(ColumnID); v != "" {
			if row.ID, err = uuid.Parse(v); err != nil {
				return nil, fmt.Errorf("%w: line %d: invalid id %q", ErrInvalidCSV, line, v)
			}
		}
		if hasTacticalName {
			tn := &models.TacticalName{}
			for i, f := range tn.Fields() {
				*f = get(tacticalNameColumns[i])
			}
			row.TacticalName = &tn
		}
		if hasSymbol {
			sym := &models.UnitSymbol{}
			for i, f := range sym.Fields() {
				*f = get(symbolColumns[i])
			}
			row.Symbol = &sym
		}
		if hasAttributes {
			attrs := []models.UnitAttribute{}
			if v := get(ColumnAttributes); v != "" {
				if err := json.Unmarshal([]byte(v), &attrs); err != nil {
					return nil, fmt.Errorf("%w: line %d: attributes must be a JSON array of {key, type, value} objects", ErrInvalidCSV, line)
				}
			}
			row.Attributes = &attrs
		}
		if hasGroups {
			row.Groups = []string{}
			for name := range strings.SplitSeq(get(ColumnGroups), GroupSeparator) {
				if name = strings.TrimSpace(name); name != "" && !slices.Contains(row.Groups, name) {
					row.Groups = append(row.Groups, name)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ImportedUnit is a unit an import created or updated.
type ImportedUnit struct {
	Unit    *models.Unit
	Created bool
	// Changed lists the fields an update changed; it is empty for units the
	// import left as they were.
	Changed []Field
}

// Import creates or updates a unit for each row, all in one transaction: on
// any error nothing is imported, and the error names the row's line. A row
// updates the unit with its ID or, failing that, its name, and creates a new
// unit otherwise. Units synced from another instance can't be imported over
// (ErrUnitSynced). Group names must exist (ErrGroupNotFound).
func (s *Service) Import(ctx context.Context, rows []ImportRow, by *models.User) ([]ImportedUnit, error) {
	var result []ImportedUnit
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		groupIDs, err := groupIDsByName(tx, rows)
		if err != nil {
			return err
		}
		seen := map[uuid.UUID]int{}
		for _, row := range rows {
			item, err := importRow(tx, row, groupIDs, by)
			if err != nil {
				return fmt.Errorf("line %d: %w", row.Line, err)
			}
			if prev, dup := seen[item.Unit.ID]; dup {
				return fmt.Errorf("line %d: %w: the unit was already imported in line %d", row.Line, ErrInvalidCSV, prev)
			}
			seen[item.Unit.ID] = row.Line
			result = append(result, item)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range result {
		item := &result[i]
		if !item.Created && len(item.Changed) == 0 {
			continue
		}
		unit, err := s.Get(ctx, item.Unit.ID)
		if err != nil {
			return nil, err
		}
		item.Unit = unit
		typ := EventUpdated
		if item.Created {
			typ = EventCreated
		}
		s.events.Publish(Event{Type: typ, ID: unit.ID, Unit: unit})
	}
	return result, nil
}

// importRow creates or updates the unit of row within tx.
func importRow(tx *gorm.DB, row ImportRow, groupIDs map[string]uint, by *models.User) (ImportedUnit, error) {
	var ids []uint
	if row.Groups != nil {
		ids = []uint{}
		for _, name := range row.Groups {
			ids = append(ids, groupIDs[name])
		}
	}
	patch := func(in *Input) {
		in.Name = row.Name
		if row.TacticalName != nil {
			in.TacticalName = *row.TacticalName
		}
		if row.Symbol != nil {
			in.Symbol = *row.Symbol
		}
		if row.Attributes != nil {
			in.Attributes = *row.Attributes
		}
		if ids != nil {
			in.GroupIDs = ids
		}
	}

	existing, err := findImportTarget(tx, row)
	if err != nil {
		return ImportedUnit{}, err
	}
	if existing == nil {
		var in Input
		patch(&in)
		unit, err := createTx(tx, in, by)
		if err != nil {
			return ImportedUnit{}, err
		}
		return ImportedUnit{Unit: unit, Created: true}, nil
	}
	changed, err := patchTx(tx, existing.ID, patch, by)
	if err != nil {
		return ImportedUnit{}, err
	}
	return ImportedUnit{Unit: existing, Changed: changed}, nil
}

// findImportTarget returns the unit with row's ID or, failing that, its name,
// or nil if there is none.
func findImportTarget(tx *gorm.DB, row ImportRow) (*models.Unit, error) {
	var unit models.Unit
	if row.ID != uuid.Nil {
		err := tx.First(&unit, "id = ?", row.ID).Error
		if err == nil {
			return &unit, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("get unit %s: %w", row.ID, err)
		}
	}
	err := tx.First(&unit, "name = ?", strings.TrimSpace(row.Name)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get unit %q: %w", row.Name, err)
	}
	return &unit, nil
}

// groupIDsByName looks up the groups the rows name, returning
// ErrGroupNotFound with the line of the first unknown one.
func groupIDsByName(tx *gorm.DB, rows []ImportRow) (map[string]uint, error) {
	var names []string
	for _, row := range rows {
		for _, name := range row.Groups {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	ids := map[string]uint{}
	if len(names) == 0 {
		return ids, nil
	}
	var groups []models.Group
	if err := tx.Where("name IN ?", names).Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("find groups: %w", err)
	}
	for _, g := range groups {
		ids[g.Name] = g.ID
	}
	for _, row := range rows {
		for _, name := range row.Groups {
			if _, ok := ids[name]; !ok {
				return nil, fmt.Errorf("line %d: %w: %q", row.Line, ErrGroupNotFound, name)
			}
		}
	}
	return ids, nil
}
