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

func (s *Server) handleUpsertPricing(w http.ResponseWriter, r *http.Request) {
	var p storage.Pricing
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if m := chi.URLParam(r, "model"); m != "" && p.Model == "" {
		p.Model = m
	}
	if p.Model == "" {
		http.Error(w, "model required", 400)
		return
	}
	// Source preservation: editing an existing row does not change its source.
	// A row keeps "openrouter" so the next sync overwrites it with fresh
	// upstream values; only rows born from an UI add (no prior row) become
	// "manual" and stay protected from sync.
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
	if existingSource != "" {
		p.Source = existingSource
	} else {
		p.Source = "manual"
	}
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
