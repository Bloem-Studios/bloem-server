package storagesource

// SelectionTestPool exposes only the existing isolated DB lifecycle to the
// external-package library-deletion integration test.
var SelectionTestPool = storageTestPool
