-- +goose Up
-- Bloem's lifecycle receipts are removed. 20260829092200_lifecycle_request_idempotency
-- stored an Idempotency-Key replay receipt for every admin, profile and
-- registration mutation; the server no longer writes or reads them. Silo's
-- retry-safety classes and the routes' own command keys (tenant member
-- creation, people policy jobs, entitlement applies) stay. users.account_incarnation_id
-- stays: sessions and access tokens bind to it.
-- +goose StatementBegin
DROP TABLE public.lifecycle_request_receipt_targets;
DROP TABLE public.lifecycle_request_receipts;
DROP TABLE public.lifecycle_idempotency_client_release_evidence;
DROP TABLE public.lifecycle_idempotency_control;
DROP FUNCTION public.reject_unresolved_lifecycle_request_receipt();
DROP FUNCTION public.guard_lifecycle_request_receipt_target();
DROP FUNCTION public.guard_lifecycle_request_receipt();
DROP FUNCTION public.guard_lifecycle_idempotency_client_evidence();
DROP FUNCTION public.guard_lifecycle_idempotency_control();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'bloem_remove_lifecycle_receipts is forward-only; restore a backup taken before it';
END;
$$;
-- +goose StatementEnd
