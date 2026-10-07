package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Wrong semantic disposition, shape/schema precedence or serialized bounds must
// fail these assertions against the actual guarded and replacement producers.
func TestNativeOnboardingSourceConfigurationAdmission(t *testing.T) {
	m := &publicv1.PluginManifest{GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", Required: true, JsonSchema: `{"type":"object","properties":{"endpoint":{"type":"string"}},"additionalProperties":false}`}}}
	for _, tc := range []struct {
		name             string
		config           map[string]map[string]any
		install, replace string
	}{
		{"required-absence", nil, "artifact_rejected", "invalid_request"},
		{"undeclared-server-secret", map[string]map[string]any{"storage": {}, "server_settings": {"secret": "value"}}, "artifact_rejected", "invalid_request"},
		{"undeclared-provider", map[string]map[string]any{"storage": {}, "other-provider": {"secret": "value"}}, "artifact_rejected", "invalid_request"},
		{"schema-wrong-type", map[string]map[string]any{"storage": {"endpoint": 7}}, "artifact_rejected", "invalid_request"},
		{"malformed-key", map[string]map[string]any{"storage": {}, "bad\x00key": {}}, "invalid_request", "invalid_request"},
		{"malformed-key-before-required", map[string]map[string]any{"bad\x00key": {}}, "invalid_request", "invalid_request"},
		{"protobuf-unsupported", map[string]map[string]any{"storage": {"endpoint": make(chan int)}}, "invalid_request", "invalid_request"},
		{"protobuf-unsupported-undeclared", map[string]map[string]any{"storage": {}, "other-provider": {"endpoint": make(chan int)}}, "invalid_request", "invalid_request"},
		{"configure-size", map[string]map[string]any{"storage": {"endpoint": strings.Repeat("x", 1<<20)}}, "request_too_large", "request_too_large"},
		{"configure-size-before-schema", map[string]map[string]any{"storage": {"endpoint": 7, "extra": strings.Repeat("x", 1<<20)}}, "request_too_large", "request_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireCode := func(err error, want string) {
				t.Helper()
				var typed *catalog.NativeOnboardingError
				if !errors.As(err, &typed) || typed.Code != want {
					t.Errorf("actual configuration decision=%T code=%s want=%s", err, func() string {
						if typed != nil {
							return typed.Code
						}
						return ""
					}(), want)
				}
			}
			requireCode(validateNativeManagementConfig(m, tc.config), tc.replace)
			// All inputs refuse before DB work; the actual InstallAuthorized path runs
			// on a registry with no live connection, never a fabricated typed result.
			r := &NativeStorageRegistry{pool: &pgxpool.Pool{}, approved: map[string]NativeStorageArtifact{"fixture": {Manifest: m}}}
			_, err := r.InstallAuthorized(t.Context(), NativeStorageInstallRequest{ArtifactKey: "fixture", Config: tc.config}, func(context.Context, pgx.Tx) error { t.Fatal("rejected config reached authority/DB"); return nil })
			requireCode(err, tc.install)
		})
	}
	if err := validateNativeManagementConfig(m, map[string]map[string]any{"storage": {"endpoint": "configured"}}); err != nil {
		t.Fatal("legal replacement config refused")
	}
	if err := validateNativeManagementConfig(m, map[string]map[string]any{"storage": nil}); err != nil {
		t.Fatal("nil-value normalization changed")
	}
	optional := &publicv1.PluginManifest{GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", JsonSchema: `{"type":"object"}`}}}
	if err := validateNativeManagementConfig(optional, nil); err != nil {
		t.Fatal("optional empty config refused")
	}
}

func TestNativeOnboardingSourceApprovalProjection(t *testing.T) {
	approved, err := newNativeStorageApprovals(map[string]NativeStorageArtifact{"z-provider": approvedNativeFixture(), "a-provider": approvedNativeFixture()})
	if err != nil {
		t.Fatal(err)
	}
	registry := &NativeStorageRegistry{approved: approved}
	views := registry.ApprovedArtifacts()
	if len(views) != 2 || views[0].ArtifactKey != "a-provider" || views[1].ArtifactKey != "z-provider" {
		t.Fatal("startup approval projection is not sorted")
	}
	encoded, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 {
		t.Fatal("approval projection exposed extra artifact data")
	}
	for _, field := range []string{"artifact_key", "plugin_id", "version", "os", "arch"} {
		if fields[field] == nil {
			t.Fatal("approval projection omitted declared field")
		}
	}
	// ArtifactKey/PluginID/Version/OS/Arch is exactly five sanitized fields.
	views[0].PluginID = "caller-mutated"
	if registry.ApprovedArtifacts()[0].PluginID == "caller-mutated" {
		t.Fatal("approval projection aliases caller data")
	}
}
