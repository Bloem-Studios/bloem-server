package apiv2

import (
	"reflect"
	"strings"

	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/Silo-Server/silo-server/internal/workerprotocol"
	"github.com/danielgtaylor/huma/v2"
)

const workerProtocolsExtension = "x-silo-worker-protocols"

type workerProtocolRegistry struct {
	Operations []workerprotocol.Operation `json:"operations"`
	Schemas    map[string]*huma.Schema    `json:"schemas"`
}

// Worker paths cannot enter the native Paths map: identical paths can describe
// different listeners and they are not served by the API. Keep the owning
// registry in the single artifact, with locally scoped schema references.
func describeWorkerReads() workerProtocolRegistry {
	schemas := huma.NewMapRegistry("#/"+workerProtocolsExtension+"/schemas/", func(t reflect.Type, hint string) string {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		name := huma.DefaultSchemaNamer(t, hint)
		if pkg := t.PkgPath(); pkg != "" {
			parts := strings.Split(pkg, "/")
			return parts[len(parts)-1] + "_" + name
		}
		return name
	})
	reads := append(proxy.ProtocolReads(schemas), transcodenode.ProtocolReads(schemas)...)
	return workerProtocolRegistry{Operations: reads, Schemas: schemas.Map()}
}
