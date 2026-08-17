-- +goose Up
-- vondel-park growth G2: a park-sold tenant IS an organization — the
-- external_service_id claim, its slots/transcodes quota, and why it is
-- suspended (if it is) ride on the tenancy boundary that already exists
-- rather than a parallel entity. A regular (non-tenant) organization
-- carries NULL for all of these; the unique index on external_service_id
-- allows unlimited NULLs (each is distinct) while still enforcing one
-- organization per park service claim.
ALTER TABLE public.organizations
    ADD COLUMN external_operator_id text NOT NULL DEFAULT '',
    ADD COLUMN external_service_id text,
    ADD COLUMN slots integer,
    ADD COLUMN transcodes integer,
    -- '' for a non-tenant org or one that isn't suspended. For a tenant
    -- org: 'quota' lifts itself once membership returns under slots;
    -- 'admin' (park dunning) lifts only on an explicit thaw.
    ADD COLUMN suspension_reason text NOT NULL DEFAULT '';

ALTER TABLE public.organizations
    ADD CONSTRAINT organizations_slots_check CHECK (slots IS NULL OR slots > 0),
    ADD CONSTRAINT organizations_transcodes_check CHECK (transcodes IS NULL OR transcodes >= 0),
    ADD CONSTRAINT organizations_suspension_reason_check
        CHECK (suspension_reason IN ('', 'quota', 'admin'));

CREATE UNIQUE INDEX organizations_external_service_unique
    ON public.organizations (external_service_id);

-- +goose Down
DROP INDEX public.organizations_external_service_unique;
ALTER TABLE public.organizations
    DROP CONSTRAINT organizations_slots_check,
    DROP CONSTRAINT organizations_transcodes_check,
    DROP CONSTRAINT organizations_suspension_reason_check,
    DROP COLUMN external_operator_id,
    DROP COLUMN external_service_id,
    DROP COLUMN slots,
    DROP COLUMN transcodes,
    DROP COLUMN suspension_reason;
