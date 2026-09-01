package ambience

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
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

// Asset kinds the authoring side labels uploads with.
const (
	KindCampaignCard16x9 = "campaign_card_16x9"
	KindSeasonBanner     = "season_banner"
	KindSeasonSprite     = "season_sprite"
)

var validAssetKinds = map[string]bool{KindCampaignCard16x9: true, KindSeasonBanner: true, KindSeasonSprite: true}

const maxAssetIDLen = 128

// StoreRequest is one standalone artwork upload. AssetID (the authoring
// system's id) makes the upload idempotent: the same AssetID with the same
// bytes returns the existing object, a different checksum replaces it.
// AssetID and Kind are optional for ad-hoc uploads.
type StoreRequest struct {
	AssetID string
	Kind    string
	Data    []byte
}

// StoredAsset describes one stored artwork object.
type StoredAsset struct {
	AssetID     string `json:"asset_id"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	URL         string `json:"url"`
	Checksum    string `json:"checksum"` // sha256 hex of the stored bytes
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// StoreAsset validates (sniffed, never the declared type) and stores an
// uploaded image, content-addressed, without attaching it to any pack. With
// an AssetID the upload is recorded in ambience_assets and upserted on retry.
func (s *Service) StoreAsset(ctx context.Context, req StoreRequest) (*StoredAsset, error) {
	if !s.HasStorage() {
		return nil, ErrStorageUnavailable
	}
	if len(req.AssetID) > maxAssetIDLen {
		return nil, invalid("asset_id must be at most %d characters", maxAssetIDLen)
	}
	if req.Kind != "" && !validAssetKinds[req.Kind] {
		return nil, invalid("kind must be one of campaign_card_16x9, season_banner, season_sprite")
	}
	if int64(len(req.Data)) > MaxAssetBytes {
		return nil, invalid("asset exceeds %d bytes", MaxAssetBytes)
	}
	contentType := strings.ToLower(http.DetectContentType(req.Data))
	ext, ok := imageExt[contentType]
	if !ok {
		return nil, ErrUnsupportedImage
	}
	sum := sha256.Sum256(req.Data)
	checksum := hex.EncodeToString(sum[:])
	out := &StoredAsset{AssetID: req.AssetID, Kind: req.Kind, Ref: checksum[:16] + ext, Checksum: checksum, ContentType: contentType, SizeBytes: int64(len(req.Data))}
	out.URL = AssetURL(out.Ref)

	if req.AssetID != "" {
		var existing StoredAsset
		err := s.pool.QueryRow(ctx, `SELECT kind, checksum, ref, content_type, size_bytes FROM ambience_assets WHERE asset_id = $1`, req.AssetID).
			Scan(&existing.Kind, &existing.Checksum, &existing.Ref, &existing.ContentType, &existing.SizeBytes)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("ambience: store asset: %w", err)
		}
		if err == nil && existing.Checksum == checksum {
			// Retry of an upload that already landed: answer without re-storing.
			existing.AssetID = req.AssetID
			existing.URL = AssetURL(existing.Ref)
			if req.Kind == "" {
				return &existing, nil
			}
			if existing.Kind != req.Kind {
				if _, err := s.pool.Exec(ctx, `UPDATE ambience_assets SET kind = $2, updated_at = now() WHERE asset_id = $1`, req.AssetID, req.Kind); err != nil {
					return nil, fmt.Errorf("ambience: store asset: %w", err)
				}
			}
			existing.Kind = req.Kind
			return &existing, nil
		}
	}
	if err := s.store.PutObject(ctx, s.store.Bucket(), assetKeyPrefix+out.Ref, req.Data); err != nil {
		return nil, err
	}
	if req.AssetID != "" {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO ambience_assets (asset_id, kind, checksum, ref, content_type, size_bytes)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (asset_id) DO UPDATE
			SET kind = EXCLUDED.kind, checksum = EXCLUDED.checksum, ref = EXCLUDED.ref,
			    content_type = EXCLUDED.content_type, size_bytes = EXCLUDED.size_bytes, updated_at = now()`,
			out.AssetID, out.Kind, out.Checksum, out.Ref, out.ContentType, out.SizeBytes); err != nil {
			return nil, fmt.Errorf("ambience: store asset: %w", err)
		}
	}
	return out, nil
}

// AttachAsset stores an uploaded image and attaches its public URL to the
// pack's slot (banner replaces, sprite appends). The pack row is locked while
// the assets column is rewritten so concurrent attaches never lose entries;
// the sprite cap is checked before anything is written to storage.
func (s *Service) AttachAsset(ctx context.Context, packID, slot string, data []byte) (*Pack, string, error) {
	if slot != SlotBanner && slot != SlotSprite {
		return nil, "", ErrInvalidSlot
	}
	if !s.HasStorage() {
		return nil, "", ErrStorageUnavailable
	}
	if slot == SlotSprite {
		pack, err := s.Get(ctx, packID)
		if err != nil {
			return nil, "", err
		}
		if len(pack.Assets.Sprites) >= maxSprites {
			return nil, "", invalid("assets.sprites holds at most %d entries", maxSprites)
		}
	} else if _, err := s.Get(ctx, packID); err != nil {
		return nil, "", err
	}
	stored, err := s.StoreAsset(ctx, StoreRequest{Data: data})
	if err != nil {
		return nil, "", err
	}
	pack, err := s.attachURL(ctx, packID, slot, stored.URL)
	if err != nil {
		return nil, "", err
	}
	return pack, stored.URL, nil
}

func (s *Service) attachURL(ctx context.Context, packID, slot, url string) (*Pack, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("ambience: attach: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT assets FROM ambience_packs WHERE id = $1 FOR UPDATE`, packID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ambience: attach: %w", err)
	}
	var assets Assets
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &assets); err != nil {
			return nil, fmt.Errorf("ambience: attach: decode assets: %w", err)
		}
	}
	if slot == SlotBanner {
		assets.BannerURL = url
	} else {
		if len(assets.Sprites) >= maxSprites {
			return nil, invalid("assets.sprites holds at most %d entries", maxSprites)
		}
		assets.Sprites = append(assets.Sprites, url)
	}
	encoded, _ := json.Marshal(assets)
	pack, err := scanPack(tx.QueryRow(ctx, `
		UPDATE ambience_packs SET assets = $2, updated_at = now() WHERE id = $1
		RETURNING `+packColumns, packID, encoded))
	if err != nil {
		return nil, fmt.Errorf("ambience: attach: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("ambience: attach: %w", err)
	}
	return pack, nil
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
