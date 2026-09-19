package scenariocatalog

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/Silo-Server/silo-server/contracts/api/v2/scenarios"
)

// These are explicit downstream decisions, not response-derived expectations.
// Load and the original Silo acceptance selectors deliberately remain unchanged.
// See docs/architecture/bloem-contract-adjudications.md.
//
//go:embed bloem_adjudications.json
var bloemAdjudicationsJSON []byte

type bloemAdjudications struct {
	SchemaVersion int                 `json:"schema_version"`
	CatalogSHA256 map[string]string   `json:"catalog_sha256"`
	Decisions     []bloemAdjudication `json:"decisions"`
}

type bloemAdjudication struct {
	Catalog           string            `json:"catalog"`
	Method            string            `json:"method"`
	Path              string            `json:"path"`
	RegistrationIndex int               `json:"registration_index"`
	Scenario          string            `json:"scenario"`
	Transport         string            `json:"transport"`
	Reason            string            `json:"reason"`
	Expect            Expect            `json:"expect"`
	Then              map[string]Expect `json:"then"`
}

// BloemCurrentCatalogs returns independent scenario copies with only the
// documented downstream expectations replaced. Requests, principals, fixture
// requirements, sequencing and frozen source files are never changed. Any
// drift in an affected baseline or supplied original is an error, not an excuse
// to silently continue applying an old decision to a new contract.
func BloemCurrentCatalogs(catalogs []*Catalog) ([]*Catalog, error) {
	var review bloemAdjudications
	decoder := json.NewDecoder(bytes.NewReader(bloemAdjudicationsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&review); err != nil {
		return nil, fmt.Errorf("decode Bloem adjudications: %w", err)
	}
	if review.SchemaVersion != 1 || len(review.Decisions) == 0 {
		return nil, fmt.Errorf("invalid Bloem adjudication version or empty decisions")
	}
	baselines := make(map[string]*Catalog, len(review.CatalogSHA256))
	for file, digest := range review.CatalogSHA256 {
		raw, err := scenarios.FS.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read adjudicated baseline %s: %w", file, err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
			return nil, fmt.Errorf("adjudicated baseline changed: %s", file)
		}
		var baseline Catalog
		if err := json.Unmarshal(raw, &baseline); err != nil {
			return nil, fmt.Errorf("decode adjudicated baseline %s: %w", file, err)
		}
		baselines[file] = &baseline
	}
	byScenario := make(map[string][]bloemAdjudication)
	seen := make(map[string]bool)
	for _, decision := range review.Decisions {
		key := decision.Catalog + ":" + decision.Scenario
		transportKey := key + ":" + decision.Transport
		baseline := baselines[decision.Catalog]
		if baseline == nil || decision.Reason == "" || seen[transportKey] || (decision.Transport != "v1" && decision.Transport != "v2") {
			return nil, fmt.Errorf("invalid or duplicate adjudication: %s", transportKey)
		}
		if _, ok := bloemAdjudicatedOriginal(baseline, decision); !ok {
			return nil, fmt.Errorf("adjudication names no exact baseline row: %s", transportKey)
		}
		seen[transportKey] = true
		byScenario[key] = append(byScenario[key], decision)
	}
	result := make([]*Catalog, 0, len(catalogs))
	for _, catalog := range catalogs {
		copyCatalog := *catalog
		copyCatalog.Rows = slices.Clone(catalog.Rows)
		for ri, row := range catalog.Rows {
			copyCatalog.Rows[ri].Scenarios = slices.Clone(row.Scenarios)
			for si, original := range row.Scenarios {
				decisions := byScenario[catalog.File+":"+original.ID]
				current := original
				if current.V2Expectation != nil {
					pair := *current.V2Expectation
					pair.Then = slices.Clone(pair.Then)
					current.V2Expectation = &pair
				}
				for _, decision := range decisions {
					baseline, _ := bloemAdjudicatedOriginal(baselines[catalog.File], decision)
					if row.Listener != "api" || row.Method != decision.Method || row.Path != decision.Path || row.RegistrationIndex != decision.RegistrationIndex || !sameSequenceShape(original, baseline) {
						return nil, fmt.Errorf("changed original for Bloem adjudication: %s/%s", catalog.File, original.ID)
					}
					if decision.Transport == "v1" {
						if len(decision.Then) != 0 {
							return nil, fmt.Errorf("unsupported v1 follow-up adjudication: %s", original.ID)
						}
						current.Expect = decision.Expect
					} else {
						if current.V2Expectation == nil {
							return nil, fmt.Errorf("missing adjudicated v2 pair: %s", original.ID)
						}
						current.V2Expectation.Expect = decision.Expect
						current.V2Expectation.Kind = "intentional_difference"
						current.V2Expectation.Summary = decision.Reason
						current.V2Expectation.RecordedIn = "internal/scenariocatalog/bloem_adjudications.json"
						for index, expectation := range decision.Then {
							i, err := strconv.Atoi(index)
							if err != nil || i < 0 || i >= len(current.V2Expectation.Then) {
								return nil, fmt.Errorf("invalid adjudicated follow-up: %s/%s", original.ID, index)
							}
							current.V2Expectation.Then[i].Expect = expectation
						}
					}
				}
				copyCatalog.Rows[ri].Scenarios[si] = current
			}
		}
		result = append(result, &copyCatalog)
	}
	return result, nil
}

func bloemAdjudicatedOriginal(catalog *Catalog, decision bloemAdjudication) (Scenario, bool) {
	for _, row := range catalog.Rows {
		if row.Listener != "api" || row.Method != decision.Method || row.Path != decision.Path || row.RegistrationIndex != decision.RegistrationIndex {
			continue
		}
		for _, scenario := range row.Scenarios {
			if scenario.ID == decision.Scenario {
				return scenario, true
			}
		}
	}
	return Scenario{}, false
}
