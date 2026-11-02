package nativestorage

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// disposableRedisOptions parses SILO_TEST_REDIS_URL and accepts it only for a
// Redis the tests may flush: no credentials, and either CI's fixed
// 127.0.0.1:65421 or the loopback address declared in
// SILO_TEST_REDIS_DISPOSABLE_ADDR by the harness that started that server.
func disposableRedisOptions(raw, declared string) (*redis.Options, error) {
	opts, err := redis.ParseURL(raw)
	if err != nil {
		return nil, err
	}
	if opts.Username != "" || opts.Password != "" {
		return nil, errors.New("a test Redis with credentials is not disposable")
	}
	if opts.Addr == "127.0.0.1:65421" {
		return opts, nil
	}
	if declared != "" && opts.Addr == declared && strings.HasPrefix(declared, "127.0.0.1:") {
		return opts, nil
	}
	return nil, fmt.Errorf("%s is not a disposable test Redis", opts.Addr)
}

// The browse Redis tests flush their server, so they accept only a Redis the
// run may destroy: CI's fixed 127.0.0.1:65421, or a loopback address the
// local gate declares for the server it started.
func TestDisposableRedisOptions(t *testing.T) {
	for _, tc := range []struct {
		name, raw, declared string
		ok                  bool
	}{
		{"CI's disposable server", "redis://127.0.0.1:65421/0", "", true},
		{"a developer's Redis", "redis://127.0.0.1:6379/0", "", false},
		{"the gate's declared server", "redis://127.0.0.1:43739/0", "127.0.0.1:43739", true},
		{"a declared address that is not the URL's", "redis://127.0.0.1:6379/0", "127.0.0.1:43739", false},
		{"a declared address off this machine", "redis://10.0.0.5:6379/0", "10.0.0.5:6379", false},
		{"credentials", "redis://user:secret@127.0.0.1:65421/0", "", false},
		{"not a URL", "::", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := disposableRedisOptions(tc.raw, tc.declared)
			if (err == nil) != tc.ok {
				t.Fatalf("disposableRedisOptions(%q, %q) error = %v, want ok=%v", tc.raw, tc.declared, err, tc.ok)
			}
		})
	}
}
