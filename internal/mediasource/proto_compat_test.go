package mediasource

import (
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// A private descriptor importing the public namespace would collide at startup.
func TestPrivateStorageDescriptorCoexists(t *testing.T) {
	_ = publicv1.GetManifestRequest{}
	_ = storagev1.DescribeRequest{}
	for _, name := range []protoreflect.FullName{"silo.plugin.v1.Runtime", "bloem.plugin.v1.StorageProvider"} {
		if _, err := protoregistry.GlobalFiles.FindDescriptorByName(name); err != nil {
			t.Fatal(err)
		}
	}
}
