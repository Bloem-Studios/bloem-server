package nativestorage

import (
	"context"
	"io"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// The same unchecked provider description is accepted only by an explicitly
// configured approved S3 installation; the default reader must refuse it.
func TestPlaybackCheckedETagRequiresExplicitS3Configuration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, plugin string
		allow, wrongSource bool
	}{
		{name: "explicit", mode: "checked-etag", plugin: "bloem.storage.s3", allow: true},
		{name: "default", plugin: "bloem.storage.s3"},
		{name: "versioned", mode: "versioned", plugin: "bloem.storage.s3"},
		{name: "unknown", mode: "etag", plugin: "bloem.storage.s3"},
		{name: "other-provider", mode: "checked-etag", plugin: "other.storage"},
		{name: "other-source", mode: "checked-etag", plugin: "bloem.storage.s3", wrongSource: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, refs, registry, runtime, _, file, authority := playbackFixture(t)
			refs.source.PluginID = tc.plugin
			registry.snapshot.Source = refs.source
			registry.snapshot.Installation.PluginID = tc.plugin
			providerID := refs.source.ProviderSourceID
			if tc.wrongSource {
				providerID = "another-source"
			}
			config, err := structpb.NewStruct(map[string]any{"source_id": providerID, "revision_mode": tc.mode})
			if err != nil {
				t.Fatal(err)
			}
			registry.snapshot.Config = []*publicv1.ConfigEntry{{Key: "connection", Value: config}}
			runtime.lease.Description.Sources[0].RevisionPinnedReads = false
			opened, err := c.OpenAuthorizedPlayback(t.Context(), file, authority)
			if !tc.allow {
				if err == nil || opened != nil {
					t.Fatal("unchecked source admitted without exact explicit S3 configuration")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			data := make([]byte, 5)
			if n, err := opened.ReadAt(data, 0); err != nil || n != len(data) || string(data) != "movie" {
				t.Fatalf("checked read: %d %q %v", n, data, err)
			}
			// Changing the opt-in is a configuration revision change in production;
			// even an inconsistent same-generation snapshot must fence this open.
			registry.snapshot.Config[0].Value.Fields["revision_mode"] = structpb.NewStringValue("versioned")
			data = []byte("stale")
			if n, err := opened.ReadAt(data, 0); err == nil || n != 0 || string(data) != "\x00\x00\x00\x00\x00" {
				t.Fatalf("changed checked admission exposed bytes: %d %q %v", n, data, err)
			}
		})
	}
}

func TestCoordinatorCheckedETagUsesSameExplicitAdmission(t *testing.T) {
	c, refs, registry, runtime, file := fixture(t)
	refs.source.PluginID = "bloem.storage.s3"
	registry.snapshot.Source = refs.source
	registry.snapshot.Installation.PluginID = refs.source.PluginID
	config, err := structpb.NewStruct(map[string]any{"source_id": refs.source.ProviderSourceID, "revision_mode": "checked-etag"})
	if err != nil {
		t.Fatal(err)
	}
	registry.snapshot.Config = []*publicv1.ConfigEntry{{Key: "connection", Value: config}}
	runtime.description.Sources[0].RevisionPinnedReads = false
	opened, err := c.OpenAuthorizedEbook(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	data, err := io.ReadAll(opened)
	if err != nil || string(data) != "book" {
		t.Fatalf("checked ebook read: %q %v", data, err)
	}
}
