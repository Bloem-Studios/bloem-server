package handlers

import (
	"encoding/json"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/plugins"
)

func TestNativeStorageArtifactResponseCarriesFormWithoutSecrets(t *testing.T) {
	view := plugins.NativeStorageArtifactView{ArtifactKey: "s3-0.3.2-linux-amd64-x", PluginID: "bloem.storage.s3", Version: "0.3.2", OS: "linux", Arch: "amd64", Catalog: true,
		ConfigSchema: []*publicv1.ConfigSchema{{Key: "connection", Title: "S3 storage connection", JsonSchema: `{"type":"object","properties":{"bucket":{"type":"string"}}}`}}}
	body, err := json.Marshal(nativeStorageArtifactsResponse{Artifacts: nativeStorageArtifactResponses([]plugins.NativeStorageArtifactView{view})})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Artifacts []struct {
			ArtifactKey  string `json:"artifact_key"`
			Catalog      bool   `json:"catalog"`
			ConfigSchema []struct {
				Key        string `json:"key"`
				JSONSchema string `json:"json_schema"`
			} `json:"config_schema"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != 1 || !got.Artifacts[0].Catalog || len(got.Artifacts[0].ConfigSchema) != 1 || got.Artifacts[0].ConfigSchema[0].Key != "connection" {
		t.Fatalf("unexpected artifacts document: %s", body)
	}
}
