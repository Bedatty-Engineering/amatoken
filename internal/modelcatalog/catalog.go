package modelcatalog

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/bedatty/amatoken/internal/storage"
)

type Entry struct {
	Model           string `json:"model"`
	Source          string `json:"source"`
	ContextLength   int64  `json:"context_length"`
	MaxOutputTokens int64  `json:"max_output_tokens"`
}

type codexModelsCache struct {
	Models []struct {
		Slug                       string `json:"slug"`
		ContextWindow              int64  `json:"context_window"`
		MaxContextWindow           int64  `json:"max_context_window"`
		MaxOutputTokens            int64  `json:"max_output_tokens"`
		MaxOutputTokensCompetitive int64  `json:"max_output_tokens_competitive"`
	} `json:"models"`
}

func Load(pricingRows []storage.Pricing, codexModelsPath string) ([]Entry, error) {
	rows := map[string]*Entry{}
	for _, p := range pricingRows {
		rows[p.Model] = &Entry{
			Model:           p.Model,
			Source:          p.Source,
			ContextLength:   p.ContextLength,
			MaxOutputTokens: p.MaxOutputTokens,
		}
	}
	if codexModelsPath != "" {
		data, err := os.ReadFile(codexModelsPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
		} else {
			var cache codexModelsCache
			if err := json.Unmarshal(data, &cache); err != nil {
				return nil, err
			}
			for _, m := range cache.Models {
				if m.Slug == "" {
					continue
				}
				row, ok := rows[m.Slug]
				if !ok {
					row = &Entry{Model: m.Slug, Source: "codex"}
					rows[m.Slug] = row
				}
				if row.ContextLength == 0 {
					row.ContextLength = m.ContextWindow
					if row.ContextLength == 0 {
						row.ContextLength = m.MaxContextWindow
					}
				}
				if row.MaxOutputTokens == 0 {
					row.MaxOutputTokens = m.MaxOutputTokens
					if row.MaxOutputTokens == 0 {
						row.MaxOutputTokens = m.MaxOutputTokensCompetitive
					}
				}
			}
		}
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContextLength != out[j].ContextLength {
			return out[i].ContextLength > out[j].ContextLength
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}
