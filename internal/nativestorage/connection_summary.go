package nativestorage

import (
	"net/url"
	"strings"
)

// ConnectionSummary returns the display-safe fields of a stored plugin
// connection. It is an allowlist per plugin: anything not named here, above all
// credentials and host paths, is never serialized. The result is never nil.
func ConnectionSummary(pluginID string, connection map[string]any) map[string]string {
	out := map[string]string{}
	text := func(key string) string { v, _ := connection[key].(string); return v }
	switch pluginID {
	case "bloem.storage.s3":
		out["prefix"] = text("prefix")
		for _, key := range []string{"source_name", "bucket", "region", "revision_mode", "probe_key"} {
			if v := text(key); v != "" {
				out[key] = v
			}
		}
		if endpoint := text("endpoint"); safeSummaryEndpoint(endpoint) {
			out["endpoint"] = endpoint
		}
		if allow, _ := connection["allow_loopback_http"].(bool); allow {
			out["allow_loopback_http"] = "true"
		}
		if len(out) == 1 && out["prefix"] == "" && text("bucket") == "" {
			return map[string]string{}
		}
	case "bloem.storage.bookwarehouse":
		if u, err := url.Parse(text("base_url")); err == nil && u.Hostname() != "" {
			out["base_url_host"] = u.Hostname()
		}
	}
	return out
}

// safeSummaryEndpoint reports whether an S3 endpoint may be shown: it must
// parse, use https, name a host and carry no userinfo. Plugin validation
// already rejects userinfo; this keeps a bad stored value from reaching a
// summary, so credentials embedded in a URL can never be echoed.
func safeSummaryEndpoint(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && !strings.Contains(raw, "@")
}
