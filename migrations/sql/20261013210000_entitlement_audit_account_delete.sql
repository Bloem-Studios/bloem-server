-- +goose Up
-- Deleting an account sets the audit events that targeted it to NULL (the
-- target_account_id foreign key is ON DELETE SET NULL), but the immutability
-- trigger rejected that update, so no account an entitlement template was ever
-- applied to could be deleted. Allow exactly that one transition; every other
-- update and every delete of an audit event is still refused.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.reject_entitlement_audit_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.target_account_id IS NOT NULL AND NEW.target_account_id IS NULL
       AND (to_jsonb(NEW) - 'target_account_id') = (to_jsonb(OLD) - 'target_account_id') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'entitlement audit events are immutable';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.reject_entitlement_audit_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'entitlement audit events are immutable';
END;
$$;
-- +goose StatementEnd
