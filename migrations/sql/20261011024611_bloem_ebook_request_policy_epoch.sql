-- +goose Up
-- A transient off/on change must invalidate work already outside a transaction.
-- These observers accept ordinary upstream writes and only advance Bloem state.
CREATE TABLE bloem_ebook_request_policy_epoch (
 id boolean PRIMARY KEY DEFAULT true CHECK (id),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision>0)
);
INSERT INTO bloem_ebook_request_policy_epoch(id) VALUES(true);

-- +goose StatementBegin
CREATE FUNCTION bloem_ebook_request_advance_policy_epoch() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 UPDATE bloem_ebook_request_policy_epoch SET revision=revision+1 WHERE id;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bloem_ebook_request_opt_in_insert
AFTER INSERT ON server_settings FOR EACH ROW
WHEN (NEW.key='bloem.requests.ebooks_enabled')
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();
CREATE TRIGGER bloem_ebook_request_opt_in_update
AFTER UPDATE ON server_settings FOR EACH ROW
WHEN ((NEW.key='bloem.requests.ebooks_enabled' OR OLD.key='bloem.requests.ebooks_enabled')
 AND (NEW.key IS DISTINCT FROM OLD.key OR NEW.value IS DISTINCT FROM OLD.value))
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();
CREATE TRIGGER bloem_ebook_request_opt_in_delete
AFTER DELETE ON server_settings FOR EACH ROW
WHEN (OLD.key='bloem.requests.ebooks_enabled')
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();
CREATE TRIGGER bloem_ebook_request_global_policy_update
AFTER UPDATE OF requests_enabled ON request_settings FOR EACH ROW
WHEN (NEW.requests_enabled IS DISTINCT FROM OLD.requests_enabled)
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();

-- Recreating an unchanged mapping can reset its row-local revision; advance
-- the acquisition epoch so an old response still cannot cross that edit.
CREATE TRIGGER bloem_ebook_request_binding_insert
AFTER INSERT ON bloem_ebook_request_integrations FOR EACH ROW
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();
CREATE TRIGGER bloem_ebook_request_binding_update
AFTER UPDATE OF media_folder_id ON bloem_ebook_request_integrations FOR EACH ROW
WHEN (NEW.media_folder_id IS DISTINCT FROM OLD.media_folder_id)
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();
CREATE TRIGGER bloem_ebook_request_binding_delete
AFTER DELETE ON bloem_ebook_request_integrations FOR EACH ROW
EXECUTE FUNCTION bloem_ebook_request_advance_policy_epoch();

-- +goose Down
DROP TRIGGER bloem_ebook_request_binding_delete ON bloem_ebook_request_integrations;
DROP TRIGGER bloem_ebook_request_binding_update ON bloem_ebook_request_integrations;
DROP TRIGGER bloem_ebook_request_binding_insert ON bloem_ebook_request_integrations;
DROP TRIGGER bloem_ebook_request_global_policy_update ON request_settings;
DROP TRIGGER bloem_ebook_request_opt_in_delete ON server_settings;
DROP TRIGGER bloem_ebook_request_opt_in_update ON server_settings;
DROP TRIGGER bloem_ebook_request_opt_in_insert ON server_settings;
DROP FUNCTION bloem_ebook_request_advance_policy_epoch();
DROP TABLE bloem_ebook_request_policy_epoch;
