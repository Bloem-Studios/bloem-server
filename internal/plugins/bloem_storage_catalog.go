package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const nativeCatalogURL = "https://raw.githubusercontent.com/Bloem-Studios/bloem-plugins/main/plugins.json"
const nativeCatalogPlugin = "bloem.storage.bookwarehouse"

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

func storageCatalogURL(raw, version, file string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Path == "/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v"+version+"/"+file && version != "" && !strings.ContainsAny(version, "/\\?#")
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
	for key, record := range records {
		m := new(publicv1.PluginManifest)
		if protojson.Unmarshal(record.Manifest, m) != nil || m.GetPluginId() != nativeCatalogPlugin || key != catalogArtifactKey(m.GetVersion(), record.Checksum) || !storageCatalogURL(record.URL, m.GetVersion(), "plugin-"+record.OS+"-"+record.Arch) {
			return errors.New("invalid native catalog release")
		}
		a := NativeStorageArtifact{Manifest: m, Checksum: record.Checksum, OS: record.OS, Arch: record.Arch}
		if err := validateNativeStorageArtifact(a); err != nil {
			return err
		}
		r.approved[key] = a
	}
	r.catalogRecords = records
	return nil
}
func catalogArtifactKey(version, checksum string) string {
	return "bookwarehouse-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + "-" + checksum
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
		if release.ID != nativeCatalogPlugin {
			continue
		}
		binary, ok := release.Binaries[runtime.GOOS+"/"+runtime.GOARCH]
		if !ok {
			continue
		}
		if !storageCatalogURL(binary.URL, release.Version, "plugin-"+runtime.GOOS+"-"+runtime.GOARCH) {
			return errors.New("invalid native catalog URL")
		}
		if b, e := hex.DecodeString(binary.Checksum); e != nil || len(b) != sha256.Size || strings.ToLower(binary.Checksum) != binary.Checksum {
			return errors.New("invalid native catalog checksum")
		}
		manifestURL := "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v" + release.Version + "/manifest.json"
		b, e := r.catalogRead(ctx, manifestURL, 1<<20)
		if e != nil {
			return e
		}
		m := new(publicv1.PluginManifest)
		if protojson.Unmarshal(b, m) != nil || m.GetPluginId() != release.ID || m.GetVersion() != release.Version {
			return errors.New("invalid native catalog manifest")
		}
		m.Checksum = binary.Checksum
		a := NativeStorageArtifact{Manifest: m, Checksum: binary.Checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}
		if err := validateNativeStorageArtifact(a); err != nil {
			return err
		}
		key := catalogArtifactKey(release.Version, binary.Checksum)
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
		return nil, errors.New("catalog release not found")
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
