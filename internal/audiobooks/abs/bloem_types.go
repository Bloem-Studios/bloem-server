package abs

import (
	"strings"
)

func publicURL(baseURL, publicPath string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(publicPath, "/")
}

// Public route prefixes owned by Bloem's compatibility adapter.
const (
	canonicalAPIPrefix = "/api"
	legacyAPIPrefix    = "/abs/api"
	legacyPublicPrefix = "/abs/public"
)
