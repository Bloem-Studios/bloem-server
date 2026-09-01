package ambience

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// AssetStore is the subset of the S3 client used for pack artwork bytes; the
// public assets bucket (the same one branding uses) is the production store.
type AssetStore interface {
	PutObject(ctx context.Context, bucket, key string, data []byte) error
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
	Bucket() string
}

// Asset slots an upload can be attached to.
const (
	SlotBanner = "banner"
	SlotSprite = "sprite"
)

// MaxAssetBytes bounds one artwork upload.
const MaxAssetBytes int64 = 8 << 20

const assetKeyPrefix = "ambience/"

var (
	// ErrStorageUnavailable means no S3 store is configured: packs can only
	// reference external https URLs.
	ErrStorageUnavailable = errors.New("ambience: asset storage is not configured")
	// ErrUnsupportedImage means the upload is not a png/webp/jpeg/gif image.
	ErrUnsupportedImage = errors.New("ambience: unsupported image type")
	// ErrInvalidSlot means the slot is neither banner nor sprite.
	ErrInvalidSlot = errors.New("ambience: invalid asset slot")
	// ErrAssetNotFound means no object exists for the ref.
	ErrAssetNotFound = errors.New("ambience: asset not found")
)

// assetRefPattern is the content ref: 16 hex chars of the sha256 plus a
// vetted extension. Anchoring it keeps serve paths free of traversal.
var assetRefPattern = regexp.MustCompile(`^[0-9a-f]{16}\.(png|webp|jpg|gif)$`)

// Raster-only: SVG can carry scripts and sprites/banners never need it.
var imageExt = map[string]string{
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
}

// HasStorage reports whether artwork upload/serving is available.
func (s *Service) HasStorage() bool { return s != nil && s.store != nil }

// AssetURL is the public path for a stored artwork ref.
func AssetURL(ref string) string { return AssetURLBase + ref }

// AttachAsset stores an uploaded image (content-addressed, sniffed rather than
// trusting the declared type), attaches its public URL to the pack's slot
// (banner replaces, sprite appends), and returns the updated pack and URL.
func (s *Service) AttachAsset(ctx context.Context, packID, slot string, data []byte) (*Pack, string, error) {
	if slot != SlotBanner && slot != SlotSprite {
		return nil, "", ErrInvalidSlot
	}
	if !s.HasStorage() {
		return nil, "", ErrStorageUnavailable
	}
	if int64(len(data)) > MaxAssetBytes {
		return nil, "", invalid("asset exceeds %d bytes", MaxAssetBytes)
	}
	ext, ok := imageExt[strings.ToLower(http.DetectContentType(data))]
	if !ok {
		return nil, "", ErrUnsupportedImage
	}
	pack, err := s.Get(ctx, packID)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	ref := hex.EncodeToString(sum[:])[:16] + ext
	if err := s.store.PutObject(ctx, s.store.Bucket(), assetKeyPrefix+ref, data); err != nil {
		return nil, "", err
	}
	url := AssetURL(ref)
	assets := pack.Assets
	if slot == SlotBanner {
		assets.BannerURL = url
	} else {
		if len(assets.Sprites) >= maxSprites {
			return nil, "", invalid("assets.sprites holds at most %d entries", maxSprites)
		}
		assets.Sprites = append(append([]string(nil), assets.Sprites...), url)
	}
	updated, err := s.Update(ctx, pack.ID, Input{
		EffectID:       pack.EffectID,
		Window:         pack.Window,
		Intensity:      &pack.Intensity,
		Surfaces:       pack.Surfaces,
		Assets:         assets,
		OrganizationID: pack.OrganizationID,
	})
	if err != nil {
		return nil, "", err
	}
	return updated, url, nil
}

// ServeAsset returns the bytes and content type for a stored artwork ref.
func (s *Service) ServeAsset(ctx context.Context, ref string) ([]byte, string, error) {
	if !assetRefPattern.MatchString(ref) {
		return nil, "", ErrAssetNotFound
	}
	if !s.HasStorage() {
		return nil, "", ErrStorageUnavailable
	}
	data, err := s.store.GetObject(ctx, s.store.Bucket(), assetKeyPrefix+ref)
	if err != nil {
		return nil, "", ErrAssetNotFound
	}
	contentType := "application/octet-stream"
	for ct, ext := range imageExt {
		if ext == path.Ext(ref) {
			contentType = ct
		}
	}
	return data, contentType, nil
}
