// Package bloemsource offers the complete source code of the running build to
// the people who use the server. Bloem Server is a modified version of
// AGPL-3.0 software that people use over a network, so section 13 of the
// license asks it to offer them the Corresponding Source of the version they
// use, at no charge.
//
// A deploy writes the source archive of the commit it ships into a directory
// the server can read (BLOEM_SOURCE_ARCHIVE_DIR). The server serves only the
// archive whose name matches its own build revision, so the offer always names
// the code that is running.
package bloemsource

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/buildinfo"
)

// EnvArchiveDir names the directory that holds the source archives.
const EnvArchiveDir = "BLOEM_SOURCE_ARCHIVE_DIR"

// License is the SPDX identifier of the license the source is offered under.
const License = "AGPL-3.0"

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ArchiveName is the file name a deploy gives the archive of a revision.
func ArchiveName(revision string) string {
	return "bloem-server-" + revision + ".tar.gz"
}

// Offer locates the source archive of one build revision.
type Offer struct {
	dir      string
	revision string
}

// Archive describes the archive an Offer found on disk.
type Archive struct {
	Path      string
	Name      string
	Size      int64
	UpdatedAt time.Time
}

// New returns the offer for revision in dir, or nil when dir is empty or the
// build has no clean commit revision to match an archive against.
func New(dir string, info buildinfo.Info) *Offer {
	dir = strings.TrimSpace(dir)
	revision := strings.ToLower(strings.TrimSpace(info.Revision))
	if dir == "" || info.Dirty || !revisionPattern.MatchString(revision) {
		return nil
	}
	return &Offer{dir: dir, revision: revision}
}

// FromEnv builds the offer from BLOEM_SOURCE_ARCHIVE_DIR and the running build,
// and logs whether the archive is there, so a missing one shows at startup.
func FromEnv(logger *slog.Logger) *Offer {
	if logger == nil {
		logger = slog.Default()
	}
	dir := os.Getenv(EnvArchiveDir)
	info := buildinfo.Current()
	offer := New(dir, info)
	switch {
	case strings.TrimSpace(dir) == "":
		logger.Info("source code offer off: no archive directory configured", "env", EnvArchiveDir)
	case offer == nil:
		logger.Warn("source code offer off: this build has no clean commit revision", "revision", info.Revision, "dirty", info.Dirty)
	default:
		if _, err := offer.Archive(); err != nil {
			logger.Error("source code archive missing for the running build", "error", err)
		} else {
			logger.Info("source code offer on", "revision", offer.Revision())
		}
	}
	return offer
}

// Revision is the commit the offer serves.
func (o *Offer) Revision() string { return o.revision }

// Archive reports the archive of the offer's revision, or an error when the
// deploy did not write it.
func (o *Offer) Archive() (Archive, error) {
	name := ArchiveName(o.revision)
	path := filepath.Join(o.dir, name)
	stat, err := os.Stat(path)
	if err != nil {
		return Archive{}, fmt.Errorf("source archive %s: %w", name, err)
	}
	if !stat.Mode().IsRegular() || stat.Size() == 0 {
		return Archive{}, fmt.Errorf("source archive %s is not a non-empty file", name)
	}
	return Archive{Path: path, Name: name, Size: stat.Size(), UpdatedAt: stat.ModTime()}, nil
}
