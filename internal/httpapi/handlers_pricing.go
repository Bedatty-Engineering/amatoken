package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/bedatty/amatoken/internal/storage"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListPricing(w http.ResponseWriter, r *http.Request) {
	rates, err := s.Repo.ListPricing(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, rates)
}

// handleCreatePricing handles POST /api/pricing — strict create. Rejects with
// 409 Conflict if a row for the same model already exists (whether manual,
// openrouter or seed). Use PUT for in-place edits.
func (s *Server) handleCreatePricing(w http.ResponseWriter, r *http.Request) {
	var p storage.Pricing
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if p.Model == "" {
		http.Error(w, "model required", 400)
		return
	}
	existing, err := s.Repo.ListPricing(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, e := range existing {
		if e.Model == p.Model {
			http.Error(w, "pricing for model "+p.Model+" already exists; edit it instead", http.StatusConflict)
			return
		}
	}
	p.Source = "manual"
	p.FetchedAt = nil
	if err := s.Repo.UpsertPricing(r.Context(), p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 201, p)
}

// handleUpdatePricing handles PUT /api/pricing/{model} — in-place edit of an
// existing row. Preserves the existing source so openrouter rows you tune
// still get refreshed by the next sync; only rows that were "manual" stay
// fully under user control.
func (s *Server) handleUpdatePricing(w http.ResponseWriter, r *http.Request) {
	var p storage.Pricing
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if m := chi.URLParam(r, "model"); m != "" {
		p.Model = m // URL is authoritative for which row is being edited
	}
	if p.Model == "" {
		http.Error(w, "model required", 400)
		return
	}
	existing, err := s.Repo.ListPricing(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var existingSource string
	for _, e := range existing {
		if e.Model == p.Model {
			existingSource = e.Source
			break
		}
	}
	if existingSource == "" {
		http.Error(w, "no pricing row for "+p.Model, http.StatusNotFound)
		return
	}
	p.Source = existingSource
	p.FetchedAt = nil
	if err := s.Repo.UpsertPricing(r.Context(), p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handlePricingSync(w http.ResponseWriter, r *http.Request) {
	if s.PricingRegistry == nil {
		http.Error(w, "pricing sync not configured", 503)
		return
	}
	res, err := s.PricingRegistry.Sync(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handlePricingStatus(w http.ResponseWriter, r *http.Request) {
	if s.PricingRegistry == nil {
		writeJSON(w, 200, map[string]any{"provider": "none"})
		return
	}
	writeJSON(w, 200, s.PricingRegistry.Status())
}

func (s *Server) handleDeletePricing(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")
	if model == "" {
		http.Error(w, "model required", 400)
		return
	}
	if err := s.Repo.DeletePricing(r.Context(), model); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}
