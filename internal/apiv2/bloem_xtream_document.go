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

type BloemXtreamProviderInput struct {
	ProfileID    string `header:"X-Profile-Id" required:"true" doc:"The administrator's selected primary profile."`
	ProfileToken string `header:"X-Profile-Token" doc:"PIN verification proof when the selected profile is locked."`
	Body         BloemXtreamProviderBody
}

type BloemXtreamProviderOutput struct {
	Body livetv.Tuner
}

func registerBloemXtreamDocument(reg *Registry) {
	op := bloemChiDocumentOp(reg, "POST", "/livetv/tuners/xtream", "createBloemXtreamProvider", "livetv", "Authenticate and import an encrypted Xtream live-TV provider.", "bearerAuth", 201)
	op.Description = "Feature detection: GET /api/bloem/v1/livetv/capability advertises xtream_supported. Uses the same authenticated viewer, selected primary profile/PIN and acting-administrator gates as other Live TV administration. Administrative-context tokens do not replace those gates. Imports live channels only, not VOD or series; playback requires direct MPEG-TS. A configured SECRET_KEY cipher and PostgreSQL store are required. Credentials are write-only. Creation is not replay-safe: after an uncertain response, read /livetv/tuners and review the saved providers before trying again. An account at the same canonical base URL cannot be configured twice. Upgrade the full API/worker fleet before enabling providers."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "invalid_body or invalid_argument: invalid input, failed provider authentication or a duplicate configured provider account.",
		401: "Authenticated authority and any required profile verification are missing or expired.",
		403: "The caller lacks acting-administrator or primary-profile authority.",
		409: "limit_exceeded: the configured provider limit was reached.",
		500: "internal_error: the provider request or persistence failed; no raw provider response is returned.",
		503: "dependency_unavailable: encrypted provider storage is unavailable, or an authority dependency is unavailable.",
	})
	registerBloemChiDocument[BloemXtreamProviderInput, BloemXtreamProviderOutput](reg, op)
}
