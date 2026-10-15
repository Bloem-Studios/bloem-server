package nativestorage

import "net/url"

// ConnectionSummary returns the display-safe fields of a stored plugin
// connection. It is an allowlist per plugin: anything not named here, above all
// credentials and host paths, is never serialized. The result is never nil.
func ConnectionSummary(pluginID string, connection map[string]any) map[string]string {
	out := map[string]string{}
	text := func(key string) string { v, _ := connection[key].(string); return v }
	switch pluginID {
	case "bloem.storage.s3":
		out["prefix"] = text("prefix")
		for _, key := range []string{"source_name", "bucket", "region", "endpoint", "revision_mode", "probe_key"} {
			if v := text(key); v != "" {
				out[key] = v
			}
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
