package httpapi

import (
	"net/http"
	"time"
)

func parseRTKDateRange(r *http.Request) (from, to *time.Time) {
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse("2006-01-02", fromStr); err == nil {
			from = &t
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if t, err := time.Parse("2006-01-02", toStr); err == nil {
			end := t.Add(24*time.Hour - time.Nanosecond)
			to = &end
		}
	}
	return from, to
}

func (s *Server) handleRTKSummary(w http.ResponseWriter, r *http.Request) {
	if s.RTKReader == nil {
		payload := map[string]interface{}{
			"available":   false,
			"install_url": "https://github.com/rtk-ai/rtk",
		}
		if !s.RTKConfigured {
			payload["installed"] = false
			payload["status"] = "not_installed"
			payload["detail"] = "RTK is not installed or not configured for this amatoken instance."
		} else {
			payload["installed"] = true
			payload["status"] = "unavailable"
			if s.RTKInitError != "" {
				payload["detail"] = s.RTKInitError
			} else {
				payload["detail"] = "RTK is configured, but amatoken could not open its history database."
			}
		}
		writeJSON(w, 200, payload)
		return
	}
	from, to := parseRTKDateRange(r)
	summary, err := s.RTKReader.Summary(r.Context(), from, to)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	summary.InstallURL = "https://github.com/rtk-ai/rtk"
	writeJSON(w, 200, summary)
}

func (s *Server) handleRTKCommands(w http.ResponseWriter, r *http.Request) {
	if s.RTKReader == nil {
		writeJSON(w, 200, []struct{}{})
		return
	}
	date := r.URL.Query().Get("date") // optional YYYY-MM-DD
	from, to := parseRTKDateRange(r)
	cmds, err := s.RTKReader.Commands(r.Context(), 10, date, from, to)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, cmds)
}

func (s *Server) handleRTKTimeSeries(w http.ResponseWriter, r *http.Request) {
	if s.RTKReader == nil {
		writeJSON(w, 200, []struct{}{})
		return
	}

	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}

	from, to := parseRTKDateRange(r)

	command := r.URL.Query().Get("command")
	points, err := s.RTKReader.TimeSeries(r.Context(), bucket, from, to, command)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, 200, points)
}
