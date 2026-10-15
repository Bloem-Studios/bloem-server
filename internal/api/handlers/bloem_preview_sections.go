package handlers

import (
	"context"
	"encoding/json"

	"github.com/Silo-Server/silo-server/internal/previewcontext"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
)

func filterBloemPreviewSections(ctx context.Context, rows []sections.ResolvedSection) []sections.ResolvedSection {
	if !previewcontext.Active(ctx) {
		return rows
	}
	out := make([]sections.ResolvedSection, 0, len(rows))
	for _, row := range rows {
		switch row.SectionType {
		case sections.SectionContinueWatching, sections.SectionNextUp, sections.SectionFavorites, sections.SectionWatchlist:
			continue
		}
		recipe, ok := recipes.Get(string(row.SectionType))
		if !ok {
			continue
		}
		category := recipe.Definition().Category
		if category == recipes.CategoryPersonalized || category == recipes.CategorySocial {
			continue
		}
		var config map[string]json.RawMessage
		if len(row.Config) > 0 && json.Unmarshal(row.Config, &config) != nil {
			continue
		}
		if _, personal := config["user_collection_id"]; personal {
			continue
		}
		out = append(out, row)
	}
	return out
}
