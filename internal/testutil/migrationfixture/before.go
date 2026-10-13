// Package migrationfixture isolates historical rollback fixtures from later
// forward-only migrations. Production always uses the complete migration chain.
package migrationfixture

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const CompatibilityRemoval int64 = 20261013161500

func Before(t testing.TB, source fs.FS, version int64) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	if err := fs.WalkDir(source, "sql", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if ok {
			v, err := strconv.ParseInt(prefix, 10, 64)
			if err == nil && v >= version {
				return nil
			}
		}
		data, err := fs.ReadFile(source, path)
		if err == nil {
			out[path] = &fstest.MapFile{Data: data}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
