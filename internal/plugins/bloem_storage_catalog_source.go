package plugins

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The native storage catalog's defaults: Bloem's public plugin catalog on GitHub.
const (
	DefaultNativeCatalogURL = "https://raw.githubusercontent.com/Bloem-Studios/bloem-plugins/main/plugins.json"
	DefaultNativeReleaseURL = "https://github.com/Bloem-Studios/bloem-plugins/releases/download"
)

// DefaultNativeDownloadHosts are the hosts the default catalog and its release
// downloads use, redirects included.
var DefaultNativeDownloadHosts = []string{"github.com", "release-assets.githubusercontent.com", "raw.githubusercontent.com"}

// Environment settings for the native storage catalog. An unset or blank
// setting keeps its default.
const (
	NativeCatalogURLEnv    = "BLOEM_NATIVE_STORAGE_CATALOG_URL"
	NativeReleaseURLEnv    = "BLOEM_NATIVE_STORAGE_RELEASE_URL"
	NativeDownloadHostsEnv = "BLOEM_NATIVE_STORAGE_DOWNLOAD_HOSTS"
)

// NativeCatalogSource says where the native storage catalog and its releases
// are read from. Empty fields keep their defaults.
type NativeCatalogSource struct {
	// CatalogURL is the release inventory (plugins.json).
	CatalogURL string
	// ReleaseURL is the base of release downloads: a release's files are at
	// ReleaseURL/<tag>/<file>, where <tag> is the plugin's tag prefix and
	// version, such as bookwarehouse-v0.3.12.
	ReleaseURL string
	// Hosts lists every host a catalog request or redirect may reach. It must
	// include the hosts of CatalogURL and ReleaseURL.
	Hosts []string
}

// NativeCatalogSourceFromEnv reads the catalog source from the environment and
// checks it, so a mistake stops the server at startup rather than at the first
// catalog read.
func NativeCatalogSourceFromEnv(getenv func(string) string) (NativeCatalogSource, error) {
	source := NativeCatalogSource{
		CatalogURL: strings.TrimSpace(getenv(NativeCatalogURLEnv)),
		ReleaseURL: strings.TrimSpace(getenv(NativeReleaseURLEnv)),
	}
	for _, host := range strings.Split(getenv(NativeDownloadHostsEnv), ",") {
		if host = strings.TrimSpace(host); host != "" {
			source.Hosts = append(source.Hosts, host)
		}
	}
	if _, err := source.parse(); err != nil {
		return NativeCatalogSource{}, err
	}
	return source, nil
}

// nativeCatalogConfig is a checked NativeCatalogSource.
type nativeCatalogConfig struct {
	catalogURL string
	release    *url.URL // no trailing slash on its path
	hosts      map[string]struct{}
}

var defaultNativeCatalogConfig = func() *nativeCatalogConfig {
	c, err := NativeCatalogSource{}.parse()
	if err != nil {
		panic(err)
	}
	return c
}()

func (s NativeCatalogSource) parse() (*nativeCatalogConfig, error) {
	catalogURL, releaseURL, hosts := s.CatalogURL, s.ReleaseURL, s.Hosts
	if catalogURL == "" {
		catalogURL = DefaultNativeCatalogURL
	}
	if releaseURL == "" {
		releaseURL = DefaultNativeReleaseURL
	}
	if len(hosts) == 0 {
		hosts = DefaultNativeDownloadHosts
	}
	c := &nativeCatalogConfig{catalogURL: catalogURL, hosts: make(map[string]struct{}, len(hosts))}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || strings.ContainsAny(host, "/@?#\\ \t") {
			return nil, fmt.Errorf("%s: invalid host %q", NativeDownloadHostsEnv, host)
		}
		c.hosts[host] = struct{}{}
	}
	catalog, err := c.checkedURL(NativeCatalogURLEnv, catalogURL)
	if err != nil {
		return nil, err
	}
	if catalog.Path == "" || strings.HasSuffix(catalog.Path, "/") {
		return nil, fmt.Errorf("%s must name a file", NativeCatalogURLEnv)
	}
	if c.release, err = c.checkedURL(NativeReleaseURLEnv, strings.TrimRight(releaseURL, "/")); err != nil {
		return nil, err
	}
	return c, nil
}

// checkedURL parses an https URL on an allowed host, with no credentials,
// query or fragment.
func (c *nativeCatalogConfig) checkedURL(setting, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("%s must be an https URL without credentials, query or fragment", setting)
	}
	if !c.allowed(u) {
		return nil, fmt.Errorf("%s: host %q is not in %s", setting, u.Host, NativeDownloadHostsEnv)
	}
	return u, nil
}

func (c *nativeCatalogConfig) allowed(u *url.URL) bool {
	_, ok := c.hosts[strings.ToLower(u.Host)]
	return ok && u.Scheme == "https"
}

func (c *nativeCatalogConfig) checkRedirect(req *url.URL, hops int) error {
	if hops > 5 || !c.allowed(req) {
		return errors.New("catalog redirect rejected")
	}
	return nil
}

// releaseFile is where a release's file is downloaded from.
func (c *nativeCatalogConfig) releaseFile(spec nativeCatalogSpec, version, file string) string {
	u := *c.release
	u.Path += "/" + spec.ReleaseTag + version + "/" + file
	u.RawPath = ""
	return u.String()
}

// isReleaseFile reports whether raw is exactly releaseFile's URL for this
// release and file.
func (c *nativeCatalogConfig) isReleaseFile(spec nativeCatalogSpec, raw, version, file string) bool {
	u, err := url.Parse(raw)
	if err != nil || !validReleaseVersion(version) || u.Scheme != "https" || u.Host != c.release.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Path == c.release.Path+"/"+spec.ReleaseTag+version+"/"+file
}

// cachedReleaseFile returns where a cached record's file is downloaded from
// now. The record may have been written under another source, such as the
// default before the server moved to its own host, so only the release part of
// its path must match; the host and base come from the current source.
func (c *nativeCatalogConfig) cachedReleaseFile(spec nativeCatalogSpec, raw, version, file string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !validReleaseVersion(version) || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasSuffix(u.Path, "/"+spec.ReleaseTag+version+"/"+file) {
		return "", false
	}
	return c.releaseFile(spec, version, file), true
}

func validReleaseVersion(version string) bool {
	return version != "" && !strings.ContainsAny(version, "/\\?#")
}
