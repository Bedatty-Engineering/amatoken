package httpapi

import (
	"net/http"
	"time"
)

func (s *Server) handleRTKSummary(w http.ResponseWriter, r *http.Request) {
	if s.RTKReader == nil {
		writeJSON(w, 200, map[string]interface{}{"available": false})
		return
	}
	summary, err := s.RTKReader.Summary(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, summary)
}

func (s *Server) handleRTKCommands(w http.ResponseWriter, r *http.Request) {
	if s.RTKReader == nil {
		writeJSON(w, 200, []struct{}{})
		return
	}
	date := r.URL.Query().Get("date") // optional YYYY-MM-DD
	cmds, err := s.RTKReader.Commands(r.Context(), 10, date)
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

	var from, to *time.Time
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse("2006-01-02", fromStr); err == nil {
			from = &t
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if t, err := time.Parse("2006-01-02", toStr); err == nil {
			to = &t
		}
	}

	command := r.URL.Query().Get("command")
	points, err := s.RTKReader.TimeSeries(r.Context(), bucket, from, to, command)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, 200, points)
}
