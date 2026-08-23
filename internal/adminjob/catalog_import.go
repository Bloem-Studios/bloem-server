package adminjob

import "github.com/Silo-Server/silo-server/internal/catalogseed"

type CatalogImportRequest struct {
	SourceBucket  string                    `json:"source_bucket"`
	SourceKey     string                    `json:"source_key"`
	SourceSHA256  string                    `json:"source_sha256,omitempty"`
	SourceLabel   string                    `json:"source_label,omitempty"`
	CleanupSource bool                      `json:"cleanup_source,omitempty"`
	LocalPath     string                    `json:"local_path,omitempty"`
	Options       catalogseed.ImportOptions `json:"options"`
}
