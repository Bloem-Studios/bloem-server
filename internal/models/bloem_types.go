package models

import "github.com/google/uuid"

// bloemUUID is uuid.UUID under a name this package's Silo files can use
// without importing uuid themselves, so Bloem's fields on Silo's model
// structs are added lines only.
type bloemUUID = uuid.UUID
