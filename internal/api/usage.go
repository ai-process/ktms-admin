package api

import (
	"net/http"
	"time"

	"github.com/ai-process/ktms-admin/internal/auth"
	"github.com/ai-process/ktms-admin/internal/usage"
)

const maxRangeDays = 400

// parseDay accepts YYYY-MM-DD or a full RFC 3339 timestamp.
func parseDay(v string) (time.Time, bool) {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// usageReport serves GET /api/usage?from=&to=&group=. "to" is a calendar day
// and is inclusive of that whole day; a missing range means the last 30 days.
func (s *Server) usageReport(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	q := r.URL.Query()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from, to := today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)
	if v := q.Get("from"); v != "" {
		t, ok := parseDay(v)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "from must be YYYY-MM-DD")
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, ok := parseDay(v)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "to must be YYYY-MM-DD")
			return
		}
		to = t.AddDate(0, 0, 1)
	}
	if !to.After(from) {
		writeErr(w, http.StatusBadRequest, "bad_request", "to must not be before from")
		return
	}
	if to.Sub(from) > maxRangeDays*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "bad_request", "range is limited to 400 days")
		return
	}
	group := q.Get("group")
	if group == "" {
		group = "project"
	}
	if _, ok := usage.Dimensions[group]; !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "group must be one of project, user, model, action, vendor, none")
		return
	}
	rows, err := s.usage.Summary(r.Context(), from, to, group)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "usage_unavailable", "light-tokenmeter: "+err.Error())
		return
	}
	rep := usage.Build(rows, group, s.modelPrices(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from.Format("2006-01-02"), "to": to.AddDate(0, 0, -1).Format("2006-01-02"), "group": group, "report": rep,
	})
}
