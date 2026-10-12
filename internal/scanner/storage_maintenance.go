package scanner

// NativeMaintenanceFileScope leaves every native reference, including missing
// rows from an earlier run, to its owning location's successful completion.
type NativeMaintenanceFileScope struct{}

func (NativeMaintenanceFileScope) Predicate(alias string) string {
	return "NOT EXISTS (SELECT 1 FROM bloem_storage_file_refs native WHERE native.media_file_id=" + alias + ".id)"
}

// NativeStorageScanConfigured witnesses the catalog writer and reconciliation
// dependencies used by native ebook pages and location completion at startup.
func (s *Scanner) NativeStorageScanConfigured() bool {
	return s != nil && s.fileRepo != nil && s.itemRepo != nil && s.libraryRepo != nil
}
