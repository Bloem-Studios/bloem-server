package libraryingest

import (
	"context"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

type alternateScanModeReader interface {
	NativeLibraryScanMode(context.Context, int) (*models.MediaFolder, bool, error)
}

type nativeScanModeReader interface {
	NativeScanFolder(context.Context, int) (*models.MediaFolder, bool, error)
}

func nilModeDependency(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func (e *Executor) tryNativeModeIngest(ctx context.Context, f *models.MediaFolder, mode scopeMode) (*Result, bool, error) {
	fail := func(cause error) (*Result, bool, error) {
		if cause != nil {
			return nil, true, catalog.MapNativeOnboardingError(cause)
		}
		return nil, true, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	if ctx == nil || e == nil || f == nil || f.ID <= 0 {
		return fail(nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	var selected any
	if e.folders != nil {
		selected = e.folders
	} else {
		selected = e.scanner
	}
	// Absence permits composition selection, never a local verdict.
	// A selected typed-nil/non-reader/error is terminal.
	if nilModeDependency(selected) {
		return fail(nil)
	}
	var read func(context.Context, int) (*models.MediaFolder, bool, error)
	if e.folders != nil {
		reader, ok := selected.(nativeScanModeReader)
		if !ok {
			return fail(nil)
		}
		read = reader.NativeScanFolder
	} else {
		reader, ok := selected.(alternateScanModeReader)
		if !ok {
			return fail(nil)
		}
		read = reader.NativeLibraryScanMode
	}
	durable, native, err := read(ctx, f.ID)
	if err != nil {
		return fail(err)
	}
	if durable == nil || durable.ID != f.ID {
		return fail(nil)
	}
	if !native {
		return nil, false, nil
	}
	if mode != scopeModeLibrary || durable.Type != "ebook" ||
		!durable.Enabled || len(durable.Paths) != 0 || nilModeDependency(e.nativeIngestor) {
		return fail(nil)
	}
	// Marker/init/binding/evidence validation belongs to the real durable reader.
	result, handled, err := e.tryNativeIngest(ctx, durable, mode)
	if err != nil {
		return result, true, err
	}
	if !handled {
		return fail(nil)
	}
	return result, true, nil
}
