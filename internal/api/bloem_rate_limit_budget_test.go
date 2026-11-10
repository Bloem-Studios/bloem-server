package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/ratelimit"
)

// AuthEndpointHandler applies an endpoint budget only when the name is one
// ratelimit.DefaultConfig defines; any other name silently leaves the route
// with nothing but the shared per-IP allowance, and the admin settings cannot
// add it. Direct-profile login shipped that way. Every literal budget name the
// routers use must therefore be a default one.
func TestAuthEndpointBudgetsAreDefined(t *testing.T) {
	defaults := ratelimit.DefaultConfig().AuthEndpoints
	budget := regexp.MustCompile(`AuthEndpointHandler\("([^"]+)"\)`)
	seen := 0
	for _, dir := range []string{".", filepath.Join("..", "apiv2")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			source, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range budget.FindAllStringSubmatch(string(source), -1) {
				seen++
				if _, ok := defaults[match[1]]; !ok {
					t.Errorf("%s uses rate-limit budget %q, which ratelimit.DefaultConfig does not define", file, match[1])
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("found no AuthEndpointHandler budgets; the scan no longer matches the routers")
	}
}
