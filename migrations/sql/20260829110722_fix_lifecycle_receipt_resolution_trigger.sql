-- +goose Up

-- Deferred row triggers retain the transition row from the statement that
-- queued them. Inspect the receipt's final state instead, so a create receipt
-- may advance from binding_unresolved to completed within one transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.reject_unresolved_lifecycle_request_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.lifecycle_request_receipts AS receipt
        WHERE receipt.idempotency_key_digest = NEW.idempotency_key_digest
          AND receipt.state = 'binding_unresolved'
    ) THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_unresolved_at_commit' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.reject_unresolved_lifecycle_request_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state = 'binding_unresolved' THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_unresolved_at_commit' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
