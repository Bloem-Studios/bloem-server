package mediasource

import (
	"fmt"

	storagev1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/bloem/plugin/v1"
	"google.golang.org/protobuf/proto"
)

// ValidatePage bounds one response. Cross-page cursor history and crash-safe
// catalog reconciliation belong to the persisted scanner, not this validator.
func ValidatePage(currentCursor string, page *storagev1.ListResponse) error {
	if page == nil || len(page.GetEntries()) > 512 || proto.Size(page) > 1<<20 {
		return fmt.Errorf("invalid storage page size")
	}
	if err := validateText(currentCursor, 4096, false); err != nil {
		return err
	}
	if err := validateText(page.GetNextCursor(), 4096, false); err != nil {
		return err
	}
	if page.GetComplete() {
		if page.GetNextCursor() != "" {
			return fmt.Errorf("terminal storage page has a cursor")
		}
	} else if page.GetNextCursor() == "" || page.GetNextCursor() == currentCursor {
		return fmt.Errorf("storage cursor did not advance")
	}
	seen := make(map[string]struct{}, len(page.GetEntries()))
	for _, entry := range page.GetEntries() {
		if entry == nil {
			return fmt.Errorf("storage page contains nil entry")
		}
		if err := validateText(entry.GetId(), 1024, true); err != nil {
			return err
		}
		if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE && entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
			return fmt.Errorf("invalid storage entry kind")
		}
		info := Info{Name: entry.GetName(), LogicalPath: entry.GetLogicalPath(), Revision: entry.GetRevision(), Size: entry.GetSize()}
		if err := validateInfo(info); err != nil {
			return err
		}
		if _, duplicate := seen[entry.GetId()]; duplicate {
			return fmt.Errorf("duplicate storage entry identity")
		}
		seen[entry.GetId()] = struct{}{}
	}
	return nil
}
