package scenariocatalog

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing/fstest"

	"github.com/Silo-Server/silo-server/contracts/api/v2/scenarios"
)

// Bloem's own rows (routes Bloem registers in a Silo route group) live in
// bloem_catalogs/<listener>/<group>.json, so Silo's catalog files stay Silo's
// bytes. Each file names the Silo catalog it extends; its rows are spliced onto
// the end of that catalog's rows, exactly where Bloem used to append them, so
// the gate, the executor and the adjudication pins see the same catalog.
//
//go:embed bloem_catalogs/*/*.json
var bloemCatalogRows embed.FS

// rowsEnd is how every Silo catalog file ends: the rows array closes, and so
// does the document.
const rowsEnd = "\n  ]\n}\n"

type bloemCatalogExtension struct {
	Extends string            `json:"extends"`
	Rows    []json.RawMessage `json:"rows"`
}

// bloemScenarioFS is the scenario FS Load reads: Silo's embedded catalogs and
// fixtures, with Bloem's rows spliced in. A malformed extension surfaces as the
// load error rather than a silently smaller catalog.
func bloemScenarioFS() fs.FS {
	fsys, err := buildBloemScenarioFS()
	if err != nil {
		return errFS{err}
	}
	return fsys
}

func buildBloemScenarioFS() (fs.FS, error) {
	merged := fstest.MapFS{}
	if err := fs.WalkDir(scenarios.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := scenarios.FS.ReadFile(p)
		if err != nil {
			return err
		}
		merged[p] = &fstest.MapFile{Data: raw}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scenariocatalog: read Silo catalogs: %w", err)
	}
	extensions, err := fs.Glob(bloemCatalogRows, "bloem_catalogs/*/*.json")
	if err != nil {
		return nil, err
	}
	for _, file := range extensions {
		raw, err := bloemCatalogRows.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var ext bloemCatalogExtension
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ext); err != nil {
			return nil, fmt.Errorf("scenariocatalog: decode %s: %w", file, err)
		}
		if "bloem_catalogs/"+ext.Extends != file || len(ext.Rows) == 0 {
			return nil, fmt.Errorf("scenariocatalog: %s must extend its own path with at least one row", file)
		}
		base, ok := merged[ext.Extends]
		if !ok {
			return nil, fmt.Errorf("scenariocatalog: %s extends missing catalog %s", file, ext.Extends)
		}
		silo := base.Data
		if !bytes.HasSuffix(silo, []byte(rowsEnd)) {
			return nil, fmt.Errorf("scenariocatalog: %s no longer ends with its rows array; re-home %s", ext.Extends, file)
		}
		var out bytes.Buffer
		out.Write(silo[:len(silo)-len(rowsEnd)])
		for _, row := range ext.Rows {
			out.WriteString(",\n    ")
			out.Write(row)
		}
		out.WriteString(rowsEnd)
		merged[ext.Extends] = &fstest.MapFile{Data: out.Bytes()}
	}
	return merged, nil
}

type errFS struct{ err error }

func (e errFS) Open(string) (fs.File, error) { return nil, e.err }
