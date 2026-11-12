-- +goose Up
-- An audiobook request integration served by the native Bookwarehouse storage
-- plugin downloads into Bookwarehouse, not into a library folder, so its
-- binding to the native library has no download folders.
ALTER TABLE bloem_audiobook_request_integrations
 DROP CONSTRAINT bloem_audiobook_request_integrations_download_roots_check,
 ADD CONSTRAINT bloem_audiobook_request_integrations_download_roots_check
 CHECK (cardinality(download_roots) BETWEEN 0 AND 4);

-- +goose Down
LOCK TABLE bloem_audiobook_request_integrations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM bloem_audiobook_request_integrations WHERE cardinality(download_roots)=0) THEN
  RAISE EXCEPTION 'native audiobook request bindings retained: refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE bloem_audiobook_request_integrations
 DROP CONSTRAINT bloem_audiobook_request_integrations_download_roots_check,
 ADD CONSTRAINT bloem_audiobook_request_integrations_download_roots_check
 CHECK (cardinality(download_roots) BETWEEN 1 AND 4);
