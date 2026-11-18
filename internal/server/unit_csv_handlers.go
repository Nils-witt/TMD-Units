package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go-unit-mangement/internal/audit"
	"go-unit-mangement/internal/auth"
	"go-unit-mangement/internal/units"
)

// maxUnitCSVBytes caps the size of a CSV import.
const maxUnitCSVBytes = 4 << 20

// unitImportResponse summarizes a CSV import.
type unitImportResponse struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

// handleExportUnits serves the local units as CSV (see units.WriteCSV).
// Units synced from another instance are left out, since they can't be
// imported here. The groups column is only included for administrators.
func (s *Server) handleExportUnits(w http.ResponseWriter, r *http.Request) {
	list, err := s.units.List(r.Context())
	if err != nil {
		writeUnitError(w, err)
		return
	}
	local := list[:0]
	for _, u := range list {
		if u.SyncRemoteID == nil {
			local = append(local, u)
		}
	}
	filename := "units-" + time.Now().UTC().Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if err := units.WriteCSV(w, local, auth.UserFromContext(r.Context()).IsAdmin); err != nil {
		slog.Error("export units", "err", err)
	}
}

// handleImportUnits creates and updates units from a CSV body (see
// units.ParseCSV and units.Service.Import). It imports all rows or none.
func (s *Server) handleImportUnits(w http.ResponseWriter, r *http.Request) {
	current := auth.UserFromContext(r.Context())
	rows, err := units.ParseCSV(http.MaxBytesReader(w, r.Body, maxUnitCSVBytes))
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			writeError(w, http.StatusRequestEntityTooLarge, "the CSV file is too large")
			return
		}
		writeUnitError(w, err)
		return
	}
	for _, row := range rows {
		if row.Groups != nil && !current.IsAdmin {
			writeError(w, http.StatusForbidden, errGroupsAdminOnly.Error()+"; remove the groups column")
			return
		}
	}
	imported, err := s.units.Import(r.Context(), rows, current)
	if err != nil {
		writeUnitError(w, err)
		return
	}
	var resp unitImportResponse
	for _, item := range imported {
		target := audit.Entry{TargetType: audit.TargetUnit, TargetID: item.Unit.ID.String(), TargetName: item.Unit.Name}
		switch {
		case item.Created:
			resp.Created++
			target.Action = audit.ActionUnitCreate
			s.record(r, target)
		case len(item.Changed) > 0:
			resp.Updated++
			s.recordUnitUpdate(r, item.Unit, item.Changed)
		default:
			resp.Unchanged++
		}
	}
	slog.Info("units imported", "by", current.Username, "created", resp.Created, "updated", resp.Updated, "unchanged", resp.Unchanged)
	writeJSON(w, http.StatusOK, resp)
}
