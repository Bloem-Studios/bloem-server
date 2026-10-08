package metadata

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// StorageCoverSource resolves storage cover paths
// (artworkkey.StorageCoverScheme) to the signed server artwork route, which
// reads the cover through its storage plugin. Those covers are never in
// artwork storage, so they use the server route whichever backend stores
// other artwork. It is registered as an image resolver source for the scheme.
type StorageCoverSource struct{ signer artworkurl.Resolver }

// NewStorageCoverSource signs storage cover keys with resolver.
func NewStorageCoverSource(resolver artworkurl.Resolver) *StorageCoverSource {
	return &StorageCoverSource{signer: resolver}
}

// StorageCoverRegistration registers source for the storage cover scheme.
func StorageCoverRegistration(source *StorageCoverSource) PluginImageResolverSourceRegistration {
	return PluginImageResolverSourceRegistration{Scheme: artworkkey.StorageCoverScheme, Source: source, Kind: PluginImageResolverSourceExplicit}
}

func (s *StorageCoverSource) ResolveImageURL(ctx context.Context, path, variant string) (string, error) {
	resolved, err := s.ResolveImageURLWithExpiry(ctx, path, variant)
	return resolved.URL, err
}

func (s *StorageCoverSource) ResolveImageURLs(ctx context.Context, paths []string, variant string) (map[string]string, error) {
	resolved, err := s.ResolveImageURLsWithExpiry(ctx, paths, variant)
	out := make(map[string]string, len(resolved))
	for path, value := range resolved {
		out[path] = value.URL
	}
	return out, err
}

func (s *StorageCoverSource) ResolveImageURLWithExpiry(ctx context.Context, path, variant string) (catalog.ResolvedImageURL, error) {
	resolved, err := s.ResolveImageURLsWithExpiry(ctx, []string{path}, variant)
	return resolved[path], err
}

// ResolveImageURLsWithExpiry signs each valid storage cover key; the variant
// is ignored because the source serves one cover size.
func (s *StorageCoverSource) ResolveImageURLsWithExpiry(ctx context.Context, paths []string, _ string) (map[string]catalog.ResolvedImageURL, error) {
	out := map[string]catalog.ResolvedImageURL{}
	if s == nil || s.signer == nil {
		return out, nil
	}
	keys := make([]string, 0, len(paths))
	for _, key := range paths {
		if _, _, ok := artworkkey.ParseStorageCoverKey(key); ok {
			keys = append(keys, key)
		}
	}
	for key, value := range s.signer.ResolveURLs(ctx, keys) {
		if value.URL != "" {
			out[key] = value
		}
	}
	return out, nil
}
