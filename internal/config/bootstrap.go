package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// minSecretKeyLen is the minimum acceptable SECRET_KEY length (in characters).
// It mirrors secret.MinMasterKeyLen; the package is not imported here to keep
// the bootstrap loader dependency-free, so the two constants must stay in sync.
const minSecretKeyLen = 32

// BootstrapConfig holds the minimal config needed before database connection.
type BootstrapConfig struct {
	InitialPlaybackEnabled           bool
	InitialPlaybackReconcileAccounts []int
	DatabaseURL                      string
	RedisURL                         string // optional override; empty means use DB setting
	Listen                           string
	JFListen                         string
	Mode                             string
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

	jfPort := os.Getenv("JF_PORT")
	if jfPort == "" {
		jfPort = "8096"
	}

	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "integrated"
	}

	initialEnabled, initialAccounts, err := initialPlaybackBootstrapForMode(mode, os.Getenv("SILO_INITIAL_PLAYBACK_ENABLED"), os.Getenv("SILO_INITIAL_PLAYBACK_RECONCILE_ACCOUNTS"))
	if err != nil {
		return nil, err
	}
	redisURL := os.Getenv("REDIS_URL")

	return &BootstrapConfig{
		InitialPlaybackEnabled:           initialEnabled,
		InitialPlaybackReconcileAccounts: initialAccounts,
		DatabaseURL:                      dbURL,
		RedisURL:                         redisURL,
		Listen:                           ":" + port,
		JFListen:                         ":" + jfPort,
		Mode:                             mode,
		SecretKey:                        []byte(secretKey),
	}, nil
}

// Workers serve captured authority but do not reconcile account sinks.
func initialPlaybackBootstrapForMode(mode, enabled, accounts string) (bool, []int, error) {
	if mode == "proxy" || mode == "transcode" {
		if accounts != "" {
			return false, nil, fmt.Errorf("worker initial playback does not reconcile accounts")
		}
		switch enabled {
		case "", "false":
			return false, nil, nil
		case "true":
			return true, nil, nil
		default:
			return false, nil, fmt.Errorf("SILO_INITIAL_PLAYBACK_ENABLED must be true or false")
		}
	}
	if enabled == "true" && mode != "integrated" && mode != "api" {
		return false, nil, fmt.Errorf("unsupported initial playback mode")
	}
	return initialPlaybackBootstrap(enabled, accounts)
}

// Reconciliation scope never enrolls an account or limits who can start playback.
func initialPlaybackBootstrap(enabled, accountList string) (bool, []int, error) {
	switch enabled {
	case "", "false":
		if accountList != "" {
			return false, nil, fmt.Errorf("SILO_INITIAL_PLAYBACK_RECONCILE_ACCOUNTS requires SILO_INITIAL_PLAYBACK_ENABLED=true")
		}
		return false, nil, nil
	case "true":
	default:
		return false, nil, fmt.Errorf("SILO_INITIAL_PLAYBACK_ENABLED must be true or false")
	}
	var accounts []int
	seen := make(map[int]bool)
	for part := range strings.SplitSeq(accountList, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 || seen[id] {
			return false, nil, fmt.Errorf("SILO_INITIAL_PLAYBACK_RECONCILE_ACCOUNTS requires distinct positive account IDs")
		}
		seen[id] = true
		accounts = append(accounts, id)
		if len(accounts) > 100 {
			return false, nil, fmt.Errorf("initial playback testing scope is limited to 100 reconciliation accounts")
		}
	}
	return true, accounts, nil
}
