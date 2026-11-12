// Package remotesource reads the remote media a `.strm` file points at.
//
// Bloem accepts `.strm` files that point anywhere and never filters content.
// It keeps one security default: only http and https, and no connection to an
// address on the server's own private network (loopback, link-local, private,
// CGNAT, multicast, cloud metadata) unless the platform setting
// bloem.strm.allow_private_addresses allows it. Guard enforces that at every
// connection, after DNS resolution and on every redirect hop.
//
// A remote URL is never placed in a catalog path, a client response, a
// playback recipe or a log line. Errors from this package never quote it.
package remotesource

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

// MaxStrmBytes bounds the `.strm` file Bloem reads. A URL is one line.
const MaxStrmBytes = 8 << 10

var (
	// ErrInvalidStrm is a `.strm` file whose first non-empty line is not an
	// absolute http or https URL with a host.
	ErrInvalidStrm = errors.New("remote source: the .strm file holds no http or https address")
	// ErrBlockedAddress is an address on the server's private network while
	// private addresses are not allowed, or a scheme other than http/https.
	ErrBlockedAddress = errors.New("remote source: address not allowed")
)

// IsStrmPath reports whether a path or name names a `.strm` file.
func IsStrmPath(name string) bool {
	return strings.EqualFold(path.Ext(name), ".strm")
}

// ParseStrm returns the URL a `.strm` file holds: its first non-empty line,
// trimmed. The content must be UTF-8 and at most MaxStrmBytes.
func ParseStrm(content []byte) (string, error) {
	if len(content) > MaxStrmBytes || !utf8.Valid(content) {
		return "", ErrInvalidStrm
	}
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := ValidateURL(line); err != nil {
			return "", err
		}
		return line, nil
	}
	return "", ErrInvalidStrm
}

// ValidateURL checks the parts of a URL that need no network: an absolute
// http or https URL with a host and no control characters.
func ValidateURL(raw string) error {
	if strings.ContainsFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ErrInvalidStrm
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return ErrInvalidStrm
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	default:
		return ErrInvalidStrm
	}
}

// Revision identifies one URL. A `.strm` file whose URL changes is a new
// revision of its catalog file; the URL itself never leaves the server.
func Revision(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return "url-sha256:" + hex.EncodeToString(sum[:])
}

// NameHint is the last path segment of the URL, used only as a container
// extension hint (for example .m2ts). It never leaves the server either.
func NameHint(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "remote"
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return "remote"
	}
	return base
}
