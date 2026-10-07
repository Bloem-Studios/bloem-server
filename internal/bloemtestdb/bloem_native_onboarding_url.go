package bloemtestdb

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

func nativeOnboardingCloneURL(dsn, name string) (string, error) {
	if !nativeOnboardingCloneName(name) {
		return "", fmt.Errorf("unowned private clone name")
	}
	uri, err := url.Parse(dsn)
	if err != nil || (uri.Scheme != "postgres" && uri.Scheme != "postgresql") {
		return "", fmt.Errorf("private fixture must use a PostgreSQL URL")
	}
	// pgx's ConnString retains the original parsed URL, even after its Database
	// field is changed. Rewrite the URL and remove any database query override.
	uri.Path = "/" + name
	uri.RawPath = ""
	query := uri.Query()
	query.Del("dbname")
	query.Del("database")
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

// nativeOnboardingCloneName admits only the exact UUID namespace this helper owns.
func nativeOnboardingCloneName(name string) bool {
	const prefix = "bloem_storage_test_acore_"
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+32 {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	parsed, err := uuid.Parse(suffix)
	return err == nil && parsed != uuid.Nil && strings.ReplaceAll(parsed.String(), "-", "") == suffix
}
