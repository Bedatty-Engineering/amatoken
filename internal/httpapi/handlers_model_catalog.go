package httpapi

import (
	"net/http"

	"github.com/bedatty/amatoken/internal/modelcatalog"
)

func (s *Server) modelCatalog(r *http.Request) ([]modelcatalog.Entry, error) {
	pricingRows, err := s.Repo.ListPricing(r.Context())
	if err != nil {
		return nil, err
	}
	return modelcatalog.Load(pricingRows, s.CodexModelsPath)
}

func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	rows, err := s.modelCatalog(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
