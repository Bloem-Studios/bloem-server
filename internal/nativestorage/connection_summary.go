package nativestorage

import (
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// s3LimitKeys are the S3 plugin's optional request-limit settings.
var s3LimitKeys = []string{"auth_failure_budget", "auth_failure_window_seconds", "auth_pause_seconds", "auth_pause_max_seconds",
	"max_concurrent_requests", "max_listing_requests", "max_read_mbps"}

// s3ProxySummaryKeys are the proxy fields a summary may carry; only a
// platform administrator sees them.
var s3ProxySummaryKeys = []string{"http_proxy", "http_proxy_username"}

// summaryPositiveInteger formats a stored whole number above zero. The config
// store decodes numbers as json.Number; a request decodes them as float64.
func summaryPositiveInteger(value any) (string, bool) {
	var n float64
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return "", false
		}
		n = f
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	default:
		return "", false
	}
	if n < 1 || n > 1e9 || n != math.Trunc(n) {
		return "", false
	}
	return strconv.FormatInt(int64(n), 10), true
}

// safeSummaryProxy reports whether a proxy address may be shown: an http or
// https origin with a host and no userinfo, query or fragment, as the plugin
// requires. A bad stored value is never echoed.
func safeSummaryProxy(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, "@?#") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == "" && (u.Path == "" || u.Path == "/")
}

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
		// Request limits and the proxy address and user, so an edit form can
		// show and keep them. The proxy password is never summarized.
		for _, key := range s3LimitKeys {
			if v, ok := summaryPositiveInteger(connection[key]); ok {
				out[key] = v
			}
		}
		if proxy := text("http_proxy"); safeSummaryProxy(proxy) {
			out["http_proxy"] = proxy
		}
		if user := text("http_proxy_username"); user != "" {
			out["http_proxy_username"] = user
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
