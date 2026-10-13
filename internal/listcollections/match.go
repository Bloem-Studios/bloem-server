package listcollections

import "maps"

// Candidate is supplied by the host's authorized catalog projection. Matching
// does not establish availability or ownership, or widen that projection.
type Candidate struct {
	ContentID   string
	Kind        string
	ExternalIDs map[string]string
}
type Unmatched struct {
	Position int    `json:"position"`
	Entry    Entry  `json:"entry"`
	Reason   string `json:"reason"`
}
type Matches struct {
	ContentIDs []string
	Unmatched  []Unmatched
}
type identityKey struct{ kind, provider, value string }

// MatchEntries uses exact provider identity and kind only. It never chooses by
// title, crosses book formats, or arbitrarily selects an ambiguous edition.
// Provider indexes keep the 10,000-row snapshot bound linear in identities.
func MatchEntries(entries []Entry, candidates []Candidate) (Matches, error) {
	out := Matches{ContentIDs: []string{}, Unmatched: []Unmatched{}}
	if len(entries) > MaxSnapshotItems {
		return out, ErrSnapshot
	}
	index := map[identityKey]map[string]bool{}
	byID := map[string]Candidate{}
	for _, c := range candidates {
		if c.ContentID == "" || byID[c.ContentID].ContentID != "" {
			return out, ErrSnapshot
		}
		byID[c.ContentID] = c
		for ns, id := range c.ExternalIDs {
			if id == "" {
				continue
			}
			key := identityKey{c.Kind, ns, id}
			if index[key] == nil {
				index[key] = map[string]bool{}
			}
			index[key][c.ContentID] = true
		}
	}
	seen := map[string]bool{}
	for position, e := range entries {
		if err := validateEntry(e); err != nil {
			return Matches{}, err
		}
		reason := ""
		matched := map[string]bool{}
		ambiguous := false
		for ns, id := range e.ExternalIDs {
			ids := index[identityKey{e.Kind, ns, id}]
			if len(ids) > 1 {
				ambiguous = true
			}
			for content := range ids {
				matched[content] = true
			}
		}
		var content string
		switch {
		case e.Kind == "unsupported":
			reason = e.UnsupportedReason
		case ambiguous:
			reason = "ambiguous_identity"
		case len(matched) > 1:
			reason = "conflicting_identity"
		case len(matched) == 0:
			reason = "missing"
		default:
			for id := range matched {
				content = id
			}
			for ns, id := range e.ExternalIDs {
				if actual := byID[content].ExternalIDs[ns]; actual != "" && actual != id {
					reason = "conflicting_identity"
					break
				}
			}
		}
		if reason != "" {
			e.ExternalIDs = maps.Clone(e.ExternalIDs)
			out.Unmatched = append(out.Unmatched, Unmatched{position, e, reason})
		} else if !seen[content] {
			seen[content] = true
			out.ContentIDs = append(out.ContentIDs, content)
		}
	}
	return out, nil
}
