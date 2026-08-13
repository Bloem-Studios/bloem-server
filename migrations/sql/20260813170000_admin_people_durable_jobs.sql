-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.admin_people_selections
    ADD COLUMN targets jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT admin_people_selections_target_limit
        CHECK (jsonb_typeof(targets) = 'array' AND jsonb_array_length(targets) <= 10000),
    ADD CONSTRAINT admin_people_selections_account_id_limit
        CHECK (cardinality(account_ids) <= 10000);

CREATE TABLE public.admin_people_bulk_jobs (
    job_id text PRIMARY KEY REFERENCES public.admin_jobs(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE RESTRICT,
    selection_id uuid NOT NULL REFERENCES public.admin_people_selections(id) ON DELETE RESTRICT,
    action_kind text NOT NULL CHECK (action_kind IN ('assign_group','suspend_memberships','reactivate_memberships')),
    group_id bigint,
    action_key text NOT NULL,
    actor_account_id integer NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    actor_authority text NOT NULL CHECK (actor_authority IN ('platform_admin','organization_admin')),
    request_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (selection_id, action_key),
    CHECK ((action_kind = 'assign_group' AND group_id IS NOT NULL) OR
           (action_kind <> 'assign_group' AND group_id IS NULL))
);

CREATE INDEX admin_people_bulk_jobs_organization_created_idx
    ON public.admin_people_bulk_jobs(organization_id, created_at DESC);

CREATE FUNCTION public.reject_admin_people_bulk_action_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'admin people bulk action is immutable';
END;
$$;

CREATE TRIGGER admin_people_bulk_action_immutable
BEFORE UPDATE ON public.admin_people_bulk_jobs
FOR EACH ROW EXECUTE FUNCTION public.reject_admin_people_bulk_action_update();

CREATE TABLE public.admin_people_bulk_targets (
    job_id text NOT NULL REFERENCES public.admin_people_bulk_jobs(job_id) ON DELETE CASCADE,
    account_id integer NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    snapshot jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded','skipped','failed')),
    reason text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, account_id),
    UNIQUE (job_id, ordinal)
);

CREATE INDEX admin_people_bulk_targets_pending_idx
    ON public.admin_people_bulk_targets(job_id, ordinal)
    WHERE status = 'pending';

CREATE FUNCTION public.protect_admin_people_bulk_target_snapshot()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.job_id IS DISTINCT FROM OLD.job_id
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.ordinal IS DISTINCT FROM OLD.ordinal
       OR NEW.snapshot IS DISTINCT FROM OLD.snapshot THEN
        RAISE EXCEPTION 'admin people bulk target snapshot is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER admin_people_bulk_target_snapshot_immutable
BEFORE UPDATE ON public.admin_people_bulk_targets
FOR EACH ROW EXECUTE FUNCTION public.protect_admin_people_bulk_target_snapshot();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.admin_jobs
WHERE id IN (SELECT job_id FROM public.admin_people_bulk_jobs);

DROP TABLE IF EXISTS public.admin_people_bulk_targets;
DROP FUNCTION IF EXISTS public.protect_admin_people_bulk_target_snapshot();
DROP TRIGGER IF EXISTS admin_people_bulk_action_immutable ON public.admin_people_bulk_jobs;
DROP TABLE IF EXISTS public.admin_people_bulk_jobs;
DROP FUNCTION IF EXISTS public.reject_admin_people_bulk_action_update();

ALTER TABLE public.admin_people_selections
    DROP CONSTRAINT IF EXISTS admin_people_selections_account_id_limit,
    DROP CONSTRAINT IF EXISTS admin_people_selections_target_limit,
    DROP COLUMN IF EXISTS targets;
-- +goose StatementEnd
