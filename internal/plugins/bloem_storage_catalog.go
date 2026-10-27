package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

const nativeCatalogURL = "https://raw.githubusercontent.com/Bloem-Studios/bloem-plugins/main/plugins.json"

type nativeCatalogSpec struct {
	PluginID      string
	KeyPrefix     string
	ReleaseTag    string // release tag prefix; the tag is ReleaseTag + version
	RequirePinned bool   // admit only digests already in the host's static approvals
}

var nativeCatalogSpecs = []nativeCatalogSpec{
	{PluginID: "bloem.storage.bookwarehouse", KeyPrefix: "bookwarehouse", ReleaseTag: "bookwarehouse-v"},
	{PluginID: "bloem.storage.s3", KeyPrefix: "s3", ReleaseTag: "s3-v", RequirePinned: true},
}

// pinKey identifies a statically approved artifact by plugin and digest, so one
// plugin's approval can never pin another plugin's catalog release.
type pinKey struct {
	pluginID string
	checksum string
}

// newNativeStoragePins retains each statically approved artifact under its
// (plugin id, checksum), so a pinned catalog release reuses the approved
// manifest rather than the one the catalog repository serves.
func newNativeStoragePins(approvals map[string]NativeStorageArtifact) map[pinKey]NativeStorageArtifact {
	pins := make(map[pinKey]NativeStorageArtifact, len(approvals))
	for _, a := range approvals {
		pins[pinKey{pluginID: a.Manifest.GetPluginId(), checksum: a.Checksum}] = a
	}
	return pins
}

func (r *NativeStorageRegistry) pinnedArtifact(pluginID, checksum string) (NativeStorageArtifact, bool) {
	a, ok := r.pinned[pinKey{pluginID: pluginID, checksum: checksum}]
	return a, ok
}

func nativeCatalogSpecFor(pluginID string) (nativeCatalogSpec, bool) {
	for _, s := range nativeCatalogSpecs {
		if s.PluginID == pluginID {
			return s, true
		}
	}
	return nativeCatalogSpec{}, false
}

// NativeCatalogArtifactPluginID names the plugin a catalog artifact key belongs
// to from its key prefix alone, so a caller can refuse a catalog key before
// the catalog has been fetched or the release downloaded. It does not mean the
// key is admitted.
func NativeCatalogArtifactPluginID(key string) (string, bool) {
	for _, s := range nativeCatalogSpecs {
		if strings.HasPrefix(key, s.KeyPrefix+"-") {
			return s.PluginID, true
		}
	}
	return "", false
}

type storageCatalogRecord struct {
	Manifest json.RawMessage `json:"manifest"`
	Checksum string          `json:"checksum"`
	OS       string          `json:"os"`
	Arch     string          `json:"arch"`
	URL      string          `json:"url"`
}
type storageCatalogRelease struct {
	ID       string `json:"plugin_id"`
	Version  string `json:"source_version"`
	Binaries map[string]struct {
		URL      string `json:"url"`
		Checksum string `json:"checksum"`
	} `json:"binaries"`
}

func storageCatalogURL(spec nativeCatalogSpec, raw, version, file string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Path == "/Bloem-Studios/bloem-plugins/releases/download/"+spec.ReleaseTag+version+"/"+file && version != "" && !strings.ContainsAny(version, "/\\?#")
}
func (r *NativeStorageRegistry) catalogFile() string {
	return filepath.Join(r.baseDir, "native-catalog.json")
}
func (r *NativeStorageRegistry) loadCatalog() error {
	p := r.catalogFile()
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return errors.New("invalid native catalog cache")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var records map[string]storageCatalogRecord
	if json.Unmarshal(b, &records) != nil {
		return errors.New("invalid native catalog cache")
	}
	kept := make(map[string]storageCatalogRecord, len(records))
	for key, record := range records {
		a, ok := r.loadCatalogRecord(key, &record)
		if !ok {
			continue
		}
		r.approved[key] = a
		kept[key] = record
	}
	// Skipped records are dropped, so a refresh never re-persists them.
	r.catalogRecords = kept
	return nil
}

// loadCatalogRecord admits one cached record. A record this build cannot
// admit (a plugin it does not know, a key or URL that does not match, an
// invalid artifact) is skipped rather than fatal: the cache may have been
// written by a later build, and skipping only withholds trust. An unpinned S3
// record is dropped silently, as before; other skips log the key only, never
// the manifest or configuration.
func (r *NativeStorageRegistry) loadCatalogRecord(key string, record *storageCatalogRecord) (NativeStorageArtifact, bool) {
	skip := func() (NativeStorageArtifact, bool) {
		slog.Warn("native catalog cache record skipped", "component", "plugins", "artifact_key", key)
		return NativeStorageArtifact{}, false
	}
	m := new(publicv1.PluginManifest)
	if protojson.Unmarshal(record.Manifest, m) != nil {
		return skip()
	}
	spec, ok := nativeCatalogSpecFor(m.GetPluginId())
	if !ok {
		return skip()
	}
	if spec.RequirePinned {
		// A pinned record carries the statically approved manifest, whatever
		// the cache holds, so installed-archive admission never depends on it.
		pinned, ok := r.pinnedArtifact(m.GetPluginId(), record.Checksum)
		if !ok || pinned.Manifest.GetVersion() != m.GetVersion() {
			return NativeStorageArtifact{}, false
		}
		m = proto.CloneOf(pinned.Manifest)
		raw, err := protojson.Marshal(m)
		if err != nil {
			return skip()
		}
		record.Manifest = raw
	}
	if key != catalogArtifactKey(spec, m.GetVersion(), record.Checksum) || !storageCatalogURL(spec, record.URL, m.GetVersion(), "plugin-"+record.OS+"-"+record.Arch) {
		return skip()
	}
	a := NativeStorageArtifact{Manifest: m, Checksum: record.Checksum, OS: record.OS, Arch: record.Arch}
	if validateNativeStorageArtifact(a) != nil {
		return skip()
	}
	return a, true
}
func catalogArtifactKey(spec nativeCatalogSpec, version, checksum string) string {
	return spec.KeyPrefix + "-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + "-" + checksum
}
func (r *NativeStorageRegistry) artifact(key string) (NativeStorageArtifact, bool) {
	r.catalogMu.RLock()
	defer r.catalogMu.RUnlock()
	a, ok := r.approved[key]
	return a, ok
}
func (r *NativeStorageRegistry) artifactSnapshot() map[string]NativeStorageArtifact {
	r.catalogMu.RLock()
	defer r.catalogMu.RUnlock()
	m := make(map[string]NativeStorageArtifact, len(r.approved))
	for k, a := range r.approved {
		m[k] = a
	}
	return m
}
func (r *NativeStorageRegistry) catalogRead(ctx context.Context, raw string, limit int64) ([]byte, error) {
	client := r.catalogClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 || req.URL.Scheme != "https" || !(req.URL.Host == "github.com" || req.URL.Host == "release-assets.githubusercontent.com" || req.URL.Host == "raw.githubusercontent.com") {
				return errors.New("catalog redirect rejected")
			}
			return nil
		}}
	}
	if limit <= 1<<20 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("plugin catalog unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("plugin catalog unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("invalid plugin catalog response")
	}
	return b, nil
}

// RefreshCatalog admits publisher-verified releases automatically. It does not
// grant installation or source authority; those remain transactional admin checks.
func (r *NativeStorageRegistry) RefreshCatalog(ctx context.Context) error {
	if r == nil {
		return errors.New("native storage unavailable")
	}
	r.catalogRefreshMu.Lock()
	defer r.catalogRefreshMu.Unlock()
	if time.Since(r.catalogFetched) < time.Minute {
		return nil
	}
	data, err := r.catalogRead(ctx, nativeCatalogURL, 1<<20)
	if err != nil {
		return err
	}
	var feed struct {
		Plugins []storageCatalogRelease `json:"plugins"`
	}
	if json.Unmarshal(data, &feed) != nil {
		return errors.New("invalid native catalog")
	}
	records := make(map[string]storageCatalogRecord)
	r.catalogMu.RLock()
	for k, v := range r.catalogRecords {
		records[k] = v
	}
	r.catalogMu.RUnlock()
	artifacts := make(map[string]NativeStorageArtifact)
	for _, release := range feed.Plugins {
		spec, ok := nativeCatalogSpecFor(release.ID)
		if !ok {
			continue
		}
		binary, found := release.Binaries[runtime.GOOS+"/"+runtime.GOARCH]
		if !found {
			continue
		}
		// Skip unpinned rows before any format validation: a malformed row for
		// a plugin we would not admit anyway must not abort the whole refresh.
		// A pin is the statically approved artifact, so its version must be the
		// row's version too.
		var pinned NativeStorageArtifact
		if spec.RequirePinned {
			var ok bool
			if pinned, ok = r.pinnedArtifact(release.ID, binary.Checksum); !ok || pinned.Manifest.GetVersion() != release.Version {
				continue
			}
		}
		if !storageCatalogURL(spec, binary.URL, release.Version, "plugin-"+runtime.GOOS+"-"+runtime.GOARCH) {
			return errors.New("invalid native catalog URL")
		}
		if b, e := hex.DecodeString(binary.Checksum); e != nil || len(b) != sha256.Size || strings.ToLower(binary.Checksum) != binary.Checksum {
			return errors.New("invalid native catalog checksum")
		}
		manifestURL := "https://github.com/Bloem-Studios/bloem-plugins/releases/download/" + spec.ReleaseTag + release.Version + "/manifest.json"
		b, e := r.catalogRead(ctx, manifestURL, 1<<20)
		if e != nil {
			return e
		}
		m := new(publicv1.PluginManifest)
		parseErr := protojson.Unmarshal(b, m)
		if spec.RequirePinned {
			// The served manifest is only a sanity check: a pinned release always
			// uses the statically approved manifest, so the catalog repository can
			// change neither its schema nor its metadata, and an installed archive
			// stays admissible without this cache.
			if parseErr != nil || m.GetPluginId() != release.ID || m.GetVersion() != release.Version || m.GetChecksum() != binary.Checksum {
				continue
			}
			m = proto.CloneOf(pinned.Manifest)
		} else if parseErr != nil || m.GetPluginId() != release.ID || m.GetVersion() != release.Version {
			return errors.New("invalid native catalog manifest")
		}
		m.Checksum = binary.Checksum
		a := NativeStorageArtifact{Manifest: m, Checksum: binary.Checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}
		if err := validateNativeStorageArtifact(a); err != nil {
			return err
		}
		key := catalogArtifactKey(spec, release.Version, binary.Checksum)
		raw, _ := protojson.Marshal(m)
		records[key] = storageCatalogRecord{Manifest: raw, Checksum: binary.Checksum, OS: runtime.GOOS, Arch: runtime.GOARCH, URL: binary.URL}
		artifacts[key] = a
	}
	if err := os.MkdirAll(r.baseDir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(records)
	if len(raw) > 1<<20 {
		return errors.New("native catalog cache exceeds bound")
	}
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(r.baseDir, ".native-catalog-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, r.catalogFile()); err != nil {
		return err
	}
	r.catalogMu.Lock()
	for key, a := range artifacts {
		r.approved[key] = a
	}
	r.catalogRecords = records
	r.catalogFetched = time.Now()
	r.catalogMu.Unlock()
	return nil
}
func (r *NativeStorageRegistry) CatalogBinary(ctx context.Context, key string) ([]byte, error) {
	if err := r.RefreshCatalog(ctx); err != nil {
		return nil, err
	}
	r.catalogMu.RLock()
	record, ok := r.catalogRecords[key]
	r.catalogMu.RUnlock()
	if !ok {
		// A key with no catalog release is refused like any unapproved artifact.
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	b, err := r.catalogRead(ctx, record.URL, 256<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != record.Checksum {
		return nil, errors.New("plugin checksum mismatch")
	}
	return b, nil
}

func (r *NativeStorageRegistry) RefreshAvailableCatalog(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return nil
	}
	return r.RefreshCatalog(ctx)
}
