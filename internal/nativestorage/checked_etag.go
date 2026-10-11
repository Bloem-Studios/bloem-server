package nativestorage

import (
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// ValidateReadDescription keeps strict admission by default. Checked ETag
// reads are a host-owned exception for the approved S3 adapter's exact retained
// source and explicit encrypted configuration, never a provider-wide fallback.
// The ordinary source/config/generation fences must still retain this snapshot.
func ValidateReadDescription(description *storagev1.DescribeResponse, sourceID, rootID string, snapshot *plugins.NativeStorageSnapshot) error {
	if checkedETagConfigured(snapshot, sourceID) {
		return storageplugin.ValidateCheckedETagSourceDescription(description, sourceID, rootID)
	}
	return storageplugin.ValidateSourceDescription(description, sourceID, rootID)
}

func checkedETagConfigured(snapshot *plugins.NativeStorageSnapshot, sourceID string) bool {
	if snapshot == nil || snapshot.Installation == nil || snapshot.Source.PluginID != "bloem.storage.s3" || snapshot.Installation.PluginID != snapshot.Source.PluginID || snapshot.Source.ProviderSourceID != sourceID || len(snapshot.Config) != 1 {
		return false
	}
	connection := snapshot.Config[0]
	if connection.GetKey() != "connection" || connection.GetValue() == nil {
		return false
	}
	fields := connection.Value.Fields
	return fields["revision_mode"].GetStringValue() == "checked-etag" && fields["source_id"].GetStringValue() == sourceID
}
