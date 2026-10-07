package translation

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func (s *Service) requireNativeJobPhase(ctx context.Context, job *Job) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	ids := []string{job.ContentID}
	switch job.TargetKind {
	case TargetItem:
		if job.IncludeChildren {
			seasons, err := s.content.SeasonTexts(ctx, job.ContentID)
			if err != nil {
				return err
			}
			episodes, err := s.content.EpisodeTexts(ctx, job.ContentID)
			if err != nil {
				return err
			}
			for _, child := range seasons {
				ids = append(ids, child.ContentID)
			}
			for _, child := range episodes {
				ids = append(ids, child.ContentID)
			}
		}
	case TargetSeason:
		_, parent, err := s.content.SeasonByID(ctx, job.ContentID)
		if err != nil {
			return err
		}
		if parent != "" {
			ids = append(ids, parent)
		}
	case TargetEpisode:
		_, parent, err := s.content.EpisodeByID(ctx, job.ContentID)
		if err != nil {
			return err
		}
		if parent != "" {
			ids = append(ids, parent)
		}
	}
	return catalog.RequireNativePhase(ctx, nil, catalog.NativePhaseTargets{ContentIDs: ids})
}
