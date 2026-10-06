-- +goose Up
ALTER TABLE bloem_storage_sources ADD COLUMN owner_id uuid;
UPDATE bloem_storage_sources s SET owner_id=i.owner_id FROM plugin_installations i WHERE i.id=s.installation_id;
-- Legacy unattached staging sources belonged to trusted platform callers.
UPDATE bloem_storage_sources SET owner_id=bloem_platform_resource_owner_id() WHERE owner_id IS NULL;
ALTER TABLE bloem_storage_sources
 ALTER COLUMN owner_id SET DEFAULT bloem_platform_resource_owner_id(),
 ALTER COLUMN owner_id SET NOT NULL,
 ADD CONSTRAINT bloem_storage_sources_owner_fk FOREIGN KEY(owner_id) REFERENCES resource_owners(id) ON DELETE RESTRICT,
 ADD CONSTRAINT bloem_storage_sources_installation_owner_fk FOREIGN KEY(installation_id,owner_id)
 REFERENCES plugin_installations(id,owner_id) ON DELETE SET NULL(installation_id);
CREATE INDEX bloem_storage_sources_owner_idx ON bloem_storage_sources(owner_id);
CREATE TABLE bloem_storage_installations (
 installation_id bigint PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES resource_owners(id) ON DELETE RESTRICT,
 protocol_version integer NOT NULL DEFAULT 1 CHECK(protocol_version=1),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(installation_id,owner_id) REFERENCES plugin_installations(id,owner_id) ON DELETE CASCADE
);
-- +goose Down
LOCK TABLE bloem_storage_sources,bloem_storage_installations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM bloem_storage_sources) OR EXISTS(SELECT 1 FROM bloem_storage_installations) THEN
  RAISE EXCEPTION 'native storage ownership retained: refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE bloem_storage_installations;
ALTER TABLE bloem_storage_sources DROP CONSTRAINT bloem_storage_sources_installation_owner_fk,
 DROP CONSTRAINT bloem_storage_sources_owner_fk;
DROP INDEX bloem_storage_sources_owner_idx;
ALTER TABLE bloem_storage_sources DROP COLUMN owner_id;
