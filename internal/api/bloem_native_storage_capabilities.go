package api

import "context"

// nativeStorageCapabilities reports whether this router composed the storage
// source services: the host runtime, the source management service and the
// reader that opens storage files.
type nativeStorageCapabilities struct {
	deps   Dependencies
	reader bool
}

func (c *nativeStorageCapabilities) NativeStorageReady(ctx context.Context) bool {
	if c == nil || ctx == nil || ctx.Err() != nil || !c.reader {
		return false
	}
	d := c.deps
	h := d.NativeStorageManagement
	return d.DB != nil && d.NativeStorage != nil && h != nil && h.Sources != nil && h.Registry == d.NativeStorage.Registry && d.LibraryIngester != nil
}
