package apiv2

import (
	"context"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/plugins"
)

// AuthProviderIconPublic checks the proxy's selected GET descriptor without
// dispatching plugin content. Exact/wildcard precedence belongs to the proxy.
type AuthProviderIconPublic func(context.Context, int, string) (bool, error)

func (reg *Registry) authProviderIcon(ctx context.Context, provider auth.LoginProviderInfo) string {
	const legacy = "/api/v1/plugins/"
	if !strings.HasPrefix(provider.IconURL, legacy) {
		return provider.IconURL
	}
	u, err := url.Parse(provider.IconURL)
	if err != nil || u.IsAbs() || u.Host != "" || strings.Contains(u.Path, "\\") || path.Clean(u.Path) != u.Path {
		return ""
	}
	rest, ok := strings.CutPrefix(u.Path, legacy)
	if !ok {
		return ""
	}
	id, asset, ok := strings.Cut(rest, "/")
	if !ok || provider.InstallationID <= 0 || id != strconv.Itoa(provider.InstallationID) || !strings.HasPrefix(asset, "assets/") || asset == "assets/" {
		return ""
	}
	if reg.deps.AuthProviderIconPublic == nil || reg.deps.PluginContent == nil || !reg.deps.PluginContent.ContentAvailable() {
		return ""
	}
	public, err := reg.deps.AuthProviderIconPublic(ctx, provider.InstallationID, "/"+asset)
	if err != nil || !public {
		return ""
	}
	u.Path = plugins.ContentPrefix + "/plugins/" + rest
	if u.RawPath != "" {
		u.RawPath = plugins.ContentPrefix + "/plugins/" + strings.TrimPrefix(u.RawPath, legacy)
	}
	return u.String()
}
