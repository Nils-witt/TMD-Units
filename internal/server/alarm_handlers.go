package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go-unit-mangement/internal/audit"
	"go-unit-mangement/internal/auth"
	"go-unit-mangement/internal/models"
	"go-unit-mangement/internal/units"
)

// alarmRequest is the body of POST /api/units/{id}/alarms. Lat, Lon and
// Title are pointers so a request without them is rejected instead of read
// as zero.
type alarmRequest struct {
	Lat         *float64 `json:"lat"`
	Lon         *float64 `json:"lon"`
	Title       *string  `json:"title"`
	Address     *string  `json:"address"`
	Details     *string  `json:"details"`
	ReferenceID *string  `json:"referenceId"`
	// Timestamp is when the alarm was raised; omitted means now.
	Timestamp *time.Time `json:"timestamp"`
}

type alarmResponse struct {
	ID          uuid.UUID        `json:"id"`
	UnitID      uuid.UUID        `json:"unitId"`
	Lat         float64          `json:"lat"`
	Lon         float64          `json:"lon"`
	Title       string           `json:"title"`
	Address     *string          `json:"address"`
	Details     *string          `json:"details"`
	ReferenceID *string          `json:"referenceId"`
	Timestamp   time.Time        `json:"timestamp"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	CreatedBy   *userRefResponse `json:"createdBy"`
	UpdatedBy   *userRefResponse `json:"updatedBy"`
}

func toAlarmResponse(a *models.UnitAlarm) alarmResponse {
	return alarmResponse{
		ID: a.ID, UnitID: a.UnitID, Lat: a.Latitude, Lon: a.Longitude, Title: a.Title,
		Address: a.Address, Details: a.Details, ReferenceID: a.ReferenceID, Timestamp: a.Timestamp,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		CreatedBy: toUserRef(a.CreatedBy), UpdatedBy: toUserRef(a.UpdatedBy),
	}
}

// handleCreateUnitAlarm stores an alarm for the unit: 201 for a new one, 200
// when it replaced the unit's alarm with the same reference ID.
func (s *Server) handleCreateUnitAlarm(w http.ResponseWriter, r *http.Request) {
	id, ok := unitIDFromPath(w, r)
	if !ok {
		return
	}
	var req alarmRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Lat == nil || req.Lon == nil || req.Title == nil {
		writeError(w, http.StatusBadRequest, "lat, lon and title are required")
		return
	}
	in := units.AlarmInput{
		Latitude: *req.Lat, Longitude: *req.Lon, Title: *req.Title,
		Address: req.Address, Details: req.Details, ReferenceID: req.ReferenceID,
	}
	if req.Timestamp != nil {
		in.Timestamp = *req.Timestamp
	}
	current := auth.UserFromContext(r.Context())
	alarm, created, err := s.units.RecordAlarm(r.Context(), id, in, current)
	if err != nil {
		writeUnitError(w, err)
		return
	}
	action, status := audit.ActionUnitAlarmUpdate, http.StatusOK
	if created {
		action, status = audit.ActionUnitAlarmCreate, http.StatusCreated
	}
	slog.Info("unit alarmed", "by", current.Username, "unit", alarm.Unit.Name, "alarm", alarm.ID, "created", created)
	details := map[string]any{"alarmId": alarm.ID.String(), "title": alarm.Title}
	if alarm.ReferenceID != nil {
		details["referenceId"] = *alarm.ReferenceID
	}
	s.record(r, audit.Entry{
		Action: action, TargetType: audit.TargetUnit, TargetID: id.String(), TargetName: alarm.Unit.Name,
		Details: details,
	})
	writeJSON(w, status, toAlarmResponse(alarm))
}

// handleListUnitAlarms lists the unit's alarms, newest first, with the
// limit, since and to parameters of the position history.
func (s *Server) handleListUnitAlarms(w http.ResponseWriter, r *http.Request) {
	id, ok := unitIDFromPath(w, r)
	if !ok {
		return
	}
	limit, since, to, err := parseHistoryQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	alarms, err := s.units.Alarms(r.Context(), id, limit, since, to)
	if err != nil {
		writeUnitError(w, err)
		return
	}
	resp := make([]alarmResponse, len(alarms))
	for i := range alarms {
		resp[i] = toAlarmResponse(&alarms[i])
	}
	writeJSON(w, http.StatusOK, resp)
}
