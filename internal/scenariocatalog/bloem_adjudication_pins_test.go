package scenariocatalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"testing"
)

// bloemUpdateAdjudicationPins rewrites each catalog_sha256 digest in raw to the digest of that
// baseline in fsys (Silo's catalog with Bloem's rows merged in) and changes nothing else.
func bloemUpdateAdjudicationPins(raw []byte, fsys fs.FS) ([]byte, error) {
	var review bloemAdjudications
	if err := json.Unmarshal(raw, &review); err != nil {
		return nil, fmt.Errorf("decode Bloem adjudications: %w", err)
	}
	out := raw
	for file, old := range review.CatalogSHA256 {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("read adjudicated baseline %s: %w", file, err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		if digest == old {
			continue
		}
		pin := regexp.MustCompile(`("` + regexp.QuoteMeta(file) + `"\s*:\s*")` + regexp.QuoteMeta(old) + `"`)
		if n := len(pin.FindAllIndex(out, -1)); n != 1 {
			return nil, fmt.Errorf("pin for %s found %d times", file, n)
		}
		out = pin.ReplaceAll(out, []byte("${1}"+digest+`"`))
	}
	return out, nil
}

// TestBloemAdjudicationPinsUpdate moves the pins after Silo changed a pinned scenario catalog:
//
//	BLOEM_UPDATE_ADJUDICATION_PINS=1 go test ./internal/scenariocatalog/ -run TestBloemAdjudicationPinsUpdate
//
// Then run the package's tests without the variable. They check that every adjudication still names
// an exact row of the new baseline; a decision on a row Silo changed fails there and needs a review.
func TestBloemAdjudicationPinsUpdate(t *testing.T) {
	if os.Getenv("BLOEM_UPDATE_ADJUDICATION_PINS") != "1" {
		return
	}
	const path = "bloem_adjudications.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := bloemUpdateAdjudicationPins(raw, bloemScenarioFS())
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) == string(raw) {
		t.Log("adjudication pins already current")
		return
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("adjudication pins moved; now run go test ./internal/scenariocatalog/ without BLOEM_UPDATE_ADJUDICATION_PINS")
}
