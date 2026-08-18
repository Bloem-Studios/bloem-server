package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// minSecretKeyLen is the minimum acceptable SECRET_KEY length (in characters).
// It mirrors secret.MinMasterKeyLen; the package is not imported here to keep
// the bootstrap loader dependency-free, so the two constants must stay in sync.
const minSecretKeyLen = 32

// BootstrapConfig holds the minimal config needed before database connection.
type BootstrapConfig struct {
	DatabaseURL string
	RedisURL    string // optional override; empty means use DB setting
	Listen      string
	JFListen    string
	Mode        string
	// SecretKey is the master key (raw SECRET_KEY env value) from which the
	// at-rest credential cipher derives its data key. It lives outside Postgres
	// so encrypted secrets survive a full database compromise/dump.
	SecretKey []byte
}

// LoadBootstrap loads bootstrap configuration from a .env file (if it exists)
// and environment variables. Only DATABASE_URL is required.
func LoadBootstrap(envFile string) (*BootstrapConfig, error) {
	if envFile != "" {
		_ = godotenv.Load(envFile)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required (set in .env or environment)")
	}

	// SECRET_KEY is the at-rest encryption master key. It is required: the server
	// must never fall back to a zero key or a key derived from another value, or
	// encrypted credentials would not survive a database dump. godotenv has
	// already loaded .env above, so dev sets it there.
	secretKey := os.Getenv("SECRET_KEY")
	if len(secretKey) < minSecretKeyLen {
		return nil, fmt.Errorf("SECRET_KEY is required (>=%d chars); generate one with: openssl rand -base64 48", minSecretKeyLen)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// JF_PORT is an explicit opt-in to a dedicated Jellyfin-compatibility
	// listener. Unset by default: the Jellyfin-compatible API is reached
	// through the compatibility gateway on the server's own address, and an
	// empty JFListen here means "no bootstrap override" (see its use in
	// cmd/silo/main.go), leaving the DB-configured jellyfin_compat.listen
	// value — itself empty by default — in place.
	jfListen := ""
	if jfPort := os.Getenv("JF_PORT"); jfPort != "" {
		jfListen = ":" + jfPort
	}

	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "integrated"
	}

	redisURL := os.Getenv("REDIS_URL")

	return &BootstrapConfig{
		DatabaseURL: dbURL,
		RedisURL:    redisURL,
		Listen:      ":" + port,
		JFListen:    jfListen,
		Mode:        mode,
		SecretKey:   []byte(secretKey),
	}, nil
}
