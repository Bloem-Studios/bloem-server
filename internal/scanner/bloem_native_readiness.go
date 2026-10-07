package scanner

import "github.com/jackc/pgx/v5/pgxpool"

// NativePublicationReady checks the concrete publisher and its durable stores.
// Constructor-owned repositories and the existing authorized publisher supply
// publication admission; capabilities do not create a publication permit.
func (s *Scanner) NativePublicationReady(pool *pgxpool.Pool) bool {
	return pool != nil && s.catalogScanConfigured() && !s.invalidOptionalCapabilities() && s.imageCacher != nil && s.fileRepo.Pool() == pool
}
