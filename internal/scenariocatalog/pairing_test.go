package scenariocatalog

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Silo-Server/silo-server/contracts/api/v2/scenarios"
)

func TestRequiredProfilePairingCannotShrink(t *testing.T) {
	for _, remove := range []bool{false, true} {
		catalogs, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		pilot, err := ProfileListAcceptance(catalogs)
		if err != nil {
			t.Fatal(err)
		}
		if remove {
			pilot[0].Rows[0].Scenarios = pilot[0].Rows[0].Scenarios[1:]
		} else {
			pilot[0].Rows[0].Scenarios[0].V2Expectation = nil
		}
		if _, err := ProfileListAcceptance(pilot); err == nil || !strings.Contains(err.Error(), "profiles_list.ok") {
			t.Fatalf("missing pairing accepted: %v", err)
		}
	}
}

func TestPairingSchemaAndOperation(t *testing.T) {
	for _, field := range []string{"method", "request", "expect", "operation_id", "headers", "body", "wrong_operation", "wrong_method", "wrong_path"} {
		t.Run(field, func(t *testing.T) {
			schema, _ := scenarios.FS.ReadFile(scenarios.SchemaPath)
			raw, _ := scenarios.FS.ReadFile("api/api-v1-profiles.json")
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			row := doc["rows"].([]any)[0].(map[string]any)
			pair := row["scenarios"].([]any)[0].(map[string]any)["v2_expectation"].(map[string]any)
			switch field {
			case "headers", "body":
				delete(pair["expect"].(map[string]any), field)
			case "wrong_operation":
				pair["operation_id"] = "deleteProfile"
			case "wrong_method":
				pair["method"] = "DELETE"
			case "wrong_path":
				pair["request"].(map[string]any)["path"] = "/api/v2/profiles/other"
			default:
				delete(pair, field)
			}
			raw, _ = json.Marshal(doc)
			_, err := load(fstest.MapFS{scenarios.SchemaPath: {Data: schema}, "api/api-v1-profiles.json": {Data: raw}})
			if err == nil {
				t.Fatalf("accepted invalid %s", field)
			}
		})
	}
}

func TestRequiredDevicePairingCannotShrink(t *testing.T) {
	for _, id := range RequiredDeviceListScenarios {
		for _, remove := range []bool{false, true} {
			catalogs, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			pilot, err := DeviceListAcceptance(catalogs)
			if err != nil {
				t.Fatal(err)
			}
			rows := pilot[0].Rows[0].Scenarios
			for i := range rows {
				if rows[i].ID != id {
					continue
				}
				if remove {
					pilot[0].Rows[0].Scenarios = append(rows[:i], rows[i+1:]...)
				} else {
					rows[i].V2Expectation = nil
				}
				break
			}
			if _, err := DeviceListAcceptance(pilot); err == nil || !strings.Contains(err.Error(), id) {
				t.Fatalf("missing %s accepted: %v", id, err)
			}
		}
	}
}

func TestRequiredDevicePairingRejectsDuplicate(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	pilot, err := DeviceListAcceptance(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	pilot[0].Rows[0].Scenarios = append(pilot[0].Rows[0].Scenarios, pilot[0].Rows[0].Scenarios[0])
	if _, err := DeviceListAcceptance(pilot); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestV2FollowupSchemaAndOperation(t *testing.T) {
	for _, kind := range []string{"valid", "unknown field", "wrong operation", "wrong method", "bad pointer", "authority header", "undeclared query", "too many requests"} {
		t.Run(kind, func(t *testing.T) {
			schema, _ := scenarios.FS.ReadFile(scenarios.SchemaPath)
			raw, _ := scenarios.FS.ReadFile("api/api-v1-profiles.json")
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			pair := doc["rows"].([]any)[0].(map[string]any)["scenarios"].([]any)[0].(map[string]any)["v2_expectation"].(map[string]any)
			binding := map[string]any{"pointer": "/page/next_cursor", "query": "cursor"}
			step := map[string]any{"operation_id": "listDevices", "method": "GET", "request": map[string]any{"path": "/api/v2/devices"}, "expect": map[string]any{"status": 200}, "from_previous": []any{binding}}
			switch kind {
			case "unknown field":
				binding["body_pointer"] = "/x"
			case "wrong operation":
				step["operation_id"] = "listProfiles"
			case "wrong method":
				step["method"] = "DELETE"
			case "bad pointer":
				binding["pointer"] = "/bad~2"
			case "authority header":
				delete(binding, "query")
				binding["request_header"] = "Authorization"
			case "undeclared query":
				binding["query"] = "unknown"
			case "too many requests":
				step["request"].(map[string]any)["repeat"] = 16
			}
			pair["then"] = []any{step}
			raw, _ = json.Marshal(doc)
			_, err := load(fstest.MapFS{scenarios.SchemaPath: {Data: schema}, "api/api-v1-profiles.json": {Data: raw}})
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}

func TestRequiredDeviceMutationPairingCannotShrink(t *testing.T) {
	testMutationPairingCannotShrink(t, RequiredDeviceMutationScenarios, DeviceMutationAcceptance)
}

func TestRequiredProfileMutationPairingCannotShrink(t *testing.T) {
	testMutationPairingCannotShrink(t, RequiredProfileMutationScenarios, ProfileMutationAcceptance)
}

func testMutationPairingCannotShrink(t *testing.T, required []string, selectCases func([]*Catalog) ([]*Catalog, error)) {
	t.Helper()
	for _, id := range required {
		for _, failure := range []string{"missing case", "missing pair", "missing read-after"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				pilot, err := selectCases(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range pilot {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "missing read-after":
								s.V2Expectation.Then = nil
							}
							break
						}
					}
				}
				if _, err := selectCases(pilot); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAPIKeyDeletePairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAPIKeyDeleteScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := APIKeyDeleteAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := APIKeyDeleteAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAPIKeyListPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAPIKeyListScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := APIKeyListAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := APIKeyListAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAPIKeyScopesPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAPIKeyScopesScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := APIKeyScopesAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := APIKeyScopesAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAPIKeyCreateRefusalPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAPIKeyCreateRefusalScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := APIKeyCreateRefusalAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := APIKeyCreateRefusalAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAPIKeyCreatePairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAPIKeyCreateScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := APIKeyCreateAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := APIKeyCreateAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAccountCapabilityPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAccountCapabilityScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := AccountCapabilityAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := AccountCapabilityAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredAuthProvidersPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredAuthProvidersScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := AuthProvidersAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := AuthProvidersAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredSignupStatusPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredSignupStatusScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := SignupStatusAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := SignupStatusAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredSetupStatusPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredSetupStatusScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := SetupStatusAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := SetupStatusAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredBuildInfoPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredBuildInfoScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := BuildInfoAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := BuildInfoAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredLoginSessionsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredLoginSessionsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := LoginSessionsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := LoginSessionsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredDeviceCapabilityPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredDeviceCapabilityScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := DeviceCapabilityAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := DeviceCapabilityAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredDeviceLookupPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredDeviceLookupScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := DeviceLookupAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := DeviceLookupAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredBuildAuthorityPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredBuildAuthorityScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := BuildAuthorityAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := BuildAuthorityAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredDeviceLookupErrorsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredDeviceLookupErrorsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := DeviceLookupErrorsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := DeviceLookupErrorsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredSetupRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredSetupRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := SetupRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := SetupRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredSessionDeleteRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredSessionDeleteRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := SessionDeleteRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := SessionDeleteRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredResourceRefusalPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredResourceRefusalScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := ResourceRefusalAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := ResourceRefusalAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredDeviceStartRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredDeviceStartRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := DeviceStartRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := DeviceStartRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredRefreshRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredRefreshRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := RefreshRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := RefreshRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredPasswordRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredPasswordRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := PasswordRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := PasswordRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredPasswordAuthorityPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredPasswordAuthorityScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := PasswordAuthorityAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := PasswordAuthorityAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredHardwareRefusalPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredHardwareRefusalScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := HardwareRefusalAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := HardwareRefusalAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredLogoutRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredLogoutRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := LogoutRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := LogoutRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}

func TestRequiredDeviceDenyRefusalsPairingCannotShrink(t *testing.T) {
	for _, id := range RequiredDeviceDenyRefusalsScenarios {
		for _, failure := range []string{"missing case", "missing pair", "unsupported requirements"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				catalogs, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				selected, err := DeviceDenyRefusalsAcceptance(catalogs)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range selected {
					for ri := range c.Rows {
						row := &c.Rows[ri]
						for i := range row.Scenarios {
							s := &row.Scenarios[i]
							if s.ID != id {
								continue
							}
							switch failure {
							case "missing case":
								row.Scenarios = append(row.Scenarios[:i], row.Scenarios[i+1:]...)
							case "missing pair":
								s.V2Expectation = nil
							case "unsupported requirements":
								s.Requires = []string{"unhandled"}
							}
							break
						}
					}
				}
				if _, err := DeviceDenyRefusalsAcceptance(selected); err == nil || !strings.Contains(err.Error(), id) {
					t.Fatalf("accepted %s: %v", failure, err)
				}
			})
		}
	}
}
