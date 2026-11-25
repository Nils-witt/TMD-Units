package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-unit-mangement/internal/models"
)

func TestCreateUnitAlarmRequiresFields(t *testing.T) {
	s := &Server{}
	for _, body := range []string{
		`{"lon":7,"title":"x"}`,
		`{"lat":50,"title":"x"}`,
		`{"lat":50,"lon":7}`,
		`not json`,
	} {
		id := uuid.NewString()
		r := httptest.NewRequest(http.MethodPost, "/api/units/"+id+"/alarms", strings.NewReader(body))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleCreateUnitAlarm(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
	}
}

func TestAlarmResponse(t *testing.T) {
	ref := "E-1"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a := &models.UnitAlarm{ID: uuid.New(), UnitID: uuid.New(), Latitude: 50, Longitude: 7, Title: "Brand", ReferenceID: &ref, Timestamp: ts, CreatedBy: &models.User{ID: 1, Username: "disp"}}
	resp := toAlarmResponse(a)
	if resp.Lat != 50 || resp.Lon != 7 || resp.Title != "Brand" || *resp.ReferenceID != ref || resp.Address != nil || resp.CreatedBy.Username != "disp" || resp.UpdatedBy != nil || !resp.Timestamp.Equal(ts) {
		t.Errorf("got %+v", resp)
	}
}
