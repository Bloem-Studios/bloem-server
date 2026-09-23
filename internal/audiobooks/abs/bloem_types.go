package abs

import (
	"strings"
)

func publicURL(baseURL, publicPath string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(publicPath, "/")
}
