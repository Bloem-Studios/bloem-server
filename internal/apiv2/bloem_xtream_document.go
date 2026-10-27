package apiv2

import "github.com/Silo-Server/silo-server/internal/livetv"

type BloemXtreamProviderBody struct {
	Type           string `json:"type,omitempty" enum:"xtream" doc:"Optional; this endpoint always creates an Xtream provider."`
	URL            string `json:"url" minLength:"1" maxLength:"2048" doc:"HTTP(S) provider base URL, at most 2048 bytes. No embedded credentials, query, fragment, encoded path, traversal, loopback or cloud metadata destinations. Provider redirects are refused."`
	Name           string `json:"name,omitempty" maxLength:"128" doc:"Optional public provider label, at most 128 UTF-8 bytes and no control characters. Defaults to Xtream; returned in the tuner's model field."`
	Username       string `json:"username" writeOnly:"true" minLength:"1" maxLength:"512" doc:"At most 512 UTF-8 bytes. Slash, backslash, NUL, CR, LF and dot-only path segments are not accepted. Encrypted server-side; never returned."`
	Password       string `json:"password" writeOnly:"true" minLength:"1" maxLength:"512" doc:"Same path-segment constraints as username. Encrypted server-side; never returned or passed to FFmpeg."`
	MaxConnections int    `json:"max_connections,omitempty" minimum:"0" maximum:"64" doc:"Local shared physical-connection budget; omitted or zero means one. Clamped to a positive provider-reported maximum."`
}

// BloemXtreamProviderInput carries only the body: the selected-profile
// headers are declared by bloemLiveTVDocumentOp with the other Live TV
// routes.
type BloemXtreamProviderInput struct {
	Body BloemXtreamProviderBody
}

type BloemXtreamProviderOutput struct {
	Body livetv.Tuner
}

func registerBloemXtreamDocument(reg *Registry) {
	op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners/xtream", "createBloemXtreamProvider", "Authenticate and import an encrypted Xtream live-TV provider.", 201, bloemLiveTVAdmin)
	op.Description = "Feature detection: GET /api/bloem/v1/livetv/capability advertises xtream_supported. Uses the same authenticated viewer, selected primary profile/PIN and acting-administrator gates as other Live TV administration. Administrative-context tokens do not replace those gates, and direct-profile sessions are not admitted. JSON body limit 32 KiB. Imports live channels only, not VOD or series; playback requires direct MPEG-TS. A configured SECRET_KEY cipher and PostgreSQL store are required. Credentials are write-only. Creation is not replay-safe: after an uncertain response, read /livetv/tuners and review the saved providers before trying again. An account at the same canonical base URL cannot be configured twice, and at most 64 providers can be configured. Upgrade the full API/worker fleet before enabling providers."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON or exceeds 32 KiB. invalid_argument: type is not xtream; the URL, name, credentials or max_connections are invalid; the provider refused authentication or listed no live channels; or the same provider account is already configured.",
		409: "limit_exceeded: 64 Xtream providers are already configured.",
		500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, the provider's channel list could not be read, the provider did not answer in time, or persistence failed; no raw provider response is returned.",
	})
	bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
	registerBloemChiDocument[BloemXtreamProviderInput, BloemXtreamProviderOutput](reg, op)
}
