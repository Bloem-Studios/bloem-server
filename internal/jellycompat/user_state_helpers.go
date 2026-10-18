package jellycompat

import "context"

func normalizeContentIDs(contentIDs []string) []string {
	if len(contentIDs) == 0 {
		return nil
	}

	result := make([]string, 0, len(contentIDs))
	seen := make(map[string]struct{}, len(contentIDs))
	for _, contentID := range contentIDs {
		if contentID == "" {
			continue
		}
		if _, ok := seen[contentID]; ok {
			continue
		}
		seen[contentID] = struct{}{}
		result = append(result, contentID)
	}
	return result
}

func resolveFavoritesForContentIDs(ctx context.Context, session *Session, userData UserDataService, contentIDs []string) (map[string]bool, error) {
	if userData == nil {
		return map[string]bool{}, nil
	}

	normalized := normalizeContentIDs(contentIDs)
	if len(normalized) == 0 {
		return map[string]bool{}, nil
	}

	favorites, err := userData.ListFavoritesByMediaItems(ctx, session, normalized)
	if err != nil {
		return nil, err
	}
	if favorites == nil {
		return map[string]bool{}, nil
	}
	return favorites, nil
}

func resolveProgressForContentIDs(ctx context.Context, session *Session, userData UserDataService, contentIDs []string) (map[string]*upstreamProgress, error) {
	if userData == nil {
		return map[string]*upstreamProgress{}, nil
	}

	normalized := normalizeContentIDs(contentIDs)
	if len(normalized) == 0 {
		return map[string]*upstreamProgress{}, nil
	}

	progress, err := userData.ListProgressByMediaItems(ctx, session, normalized)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		return map[string]*upstreamProgress{}, nil
	}
	return progress, nil
}

func resolveUserStateForContentIDs(ctx context.Context, session *Session, userData UserDataService, contentIDs []string) (map[string]bool, map[string]*upstreamProgress, error) {
	favorites, err := resolveFavoritesForContentIDs(ctx, session, userData, contentIDs)
	if err != nil {
		return nil, nil, err
	}
	progress, err := resolveProgressForContentIDs(ctx, session, userData, contentIDs)
	if err != nil {
		return nil, nil, err
	}
	return favorites, progress, nil
}
