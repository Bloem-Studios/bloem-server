-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.admin_people_bulk_jobs
    DROP CONSTRAINT admin_people_bulk_jobs_action_kind_check,
    DROP CONSTRAINT admin_people_bulk_jobs_check,
    ADD COLUMN action_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN preview_digest text NOT NULL DEFAULT '',
    ADD COLUMN target_cohort_id uuid,
    ADD COLUMN target_cohort_revision bigint,
    ADD COLUMN target_group_id bigint,
    ADD COLUMN attempt_count integer NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN last_error_code text NOT NULL DEFAULT '',
    ADD COLUMN cancel_requested_at timestamptz,
    ADD CONSTRAINT admin_people_bulk_jobs_action_kind_check CHECK (action_kind IN (
        'assign_group','suspend_memberships','reactivate_memberships',
        'assign_entitlement_cohort','apply_entitlement_template',
        'derive_entitlement_cohort','restore_default_entitlement'
    )),
    ADD CONSTRAINT admin_people_bulk_jobs_action_shape CHECK (
        (action_kind='assign_group' AND group_id IS NOT NULL AND target_group_id IS NULL AND action_payload='{}'::jsonb) OR
        (action_kind IN ('suspend_memberships','reactivate_memberships') AND group_id IS NULL AND target_group_id IS NULL AND action_payload='{}'::jsonb) OR
        (action_kind IN ('assign_entitlement_cohort','apply_entitlement_template','derive_entitlement_cohort','restore_default_entitlement')
            AND group_id IS NULL AND target_group_id IS NOT NULL AND jsonb_typeof(action_payload)='object'
            AND octet_length(action_payload::text) <= 16384
            AND (action_payload - ARRAY['kind','cohort_id','template_key','template_revision','name','patch','include_custom_profiles']::text[])='{}'::jsonb)
    ),
    ADD CONSTRAINT admin_people_bulk_jobs_target_cohort_shape CHECK (
        (target_cohort_id IS NULL AND target_cohort_revision IS NULL) OR
        (target_cohort_id IS NOT NULL AND target_cohort_revision > 0)
    ),
    ADD CONSTRAINT admin_people_bulk_jobs_target_group_fkey
        FOREIGN KEY (organization_id,target_group_id) REFERENCES public.access_groups(organization_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT admin_people_bulk_jobs_target_cohort_fkey
        FOREIGN KEY (target_cohort_id,organization_id) REFERENCES public.entitlement_policy_cohort_revisions(id,organization_id) ON DELETE RESTRICT;

CREATE TABLE public.admin_people_bulk_job_receipts (
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE RESTRICT,
    actor_account_id integer NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL CHECK (idempotency_key=btrim(idempotency_key) AND length(idempotency_key) BETWEEN 1 AND 128),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    job_id text NOT NULL REFERENCES public.admin_people_bulk_jobs(job_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,actor_account_id,idempotency_key)
);

CREATE INDEX admin_people_bulk_jobs_runnable_idx
    ON public.admin_people_bulk_jobs(next_attempt_at,created_at,job_id)
    WHERE cancel_requested_at IS NULL;

CREATE OR REPLACE FUNCTION public.reject_admin_people_bulk_action_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.job_id IS DISTINCT FROM OLD.job_id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.selection_reference IS DISTINCT FROM OLD.selection_reference
       OR NEW.action_kind IS DISTINCT FROM OLD.action_kind
       OR NEW.group_id IS DISTINCT FROM OLD.group_id
       OR NEW.action_key IS DISTINCT FROM OLD.action_key
       OR NEW.actor_account_id IS DISTINCT FROM OLD.actor_account_id
       OR NEW.actor_authority IS DISTINCT FROM OLD.actor_authority
       OR NEW.actor_membership_id IS DISTINCT FROM OLD.actor_membership_id
       OR NEW.actor_security_revision IS DISTINCT FROM OLD.actor_security_revision
       OR NEW.organization_policy_revision IS DISTINCT FROM OLD.organization_policy_revision
       OR NEW.request_id IS DISTINCT FROM OLD.request_id
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.action_payload IS DISTINCT FROM OLD.action_payload
       OR NEW.preview_digest IS DISTINCT FROM OLD.preview_digest
       OR NEW.target_cohort_id IS DISTINCT FROM OLD.target_cohort_id
       OR NEW.target_cohort_revision IS DISTINCT FROM OLD.target_cohort_revision
       OR NEW.target_group_id IS DISTINCT FROM OLD.target_group_id THEN
        RAISE EXCEPTION 'admin people bulk action is immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.admin_people_bulk_jobs b
        JOIN public.admin_jobs j ON j.id=b.job_id
        WHERE b.action_kind IN ('assign_entitlement_cohort','apply_entitlement_template','derive_entitlement_cohort','restore_default_entitlement')
          AND j.status IN ('queued','running')
    ) THEN
        RAISE EXCEPTION 'cannot roll back: people policy jobs are queued or running';
    END IF;
END;
$$;

DELETE FROM public.admin_jobs j USING public.admin_people_bulk_jobs b
WHERE j.id=b.job_id AND b.action_kind IN ('assign_entitlement_cohort','apply_entitlement_template','derive_entitlement_cohort','restore_default_entitlement');
DROP TABLE public.admin_people_bulk_job_receipts;
DROP INDEX public.admin_people_bulk_jobs_runnable_idx;
ALTER TABLE public.admin_people_bulk_jobs
    DROP CONSTRAINT admin_people_bulk_jobs_target_cohort_fkey,
    DROP CONSTRAINT admin_people_bulk_jobs_target_group_fkey,
    DROP CONSTRAINT admin_people_bulk_jobs_target_cohort_shape,
    DROP CONSTRAINT admin_people_bulk_jobs_action_shape,
    DROP CONSTRAINT admin_people_bulk_jobs_action_kind_check,
    DROP COLUMN cancel_requested_at,
    DROP COLUMN last_error_code,
    DROP COLUMN next_attempt_at,
    DROP COLUMN attempt_count,
    DROP COLUMN target_group_id,
    DROP COLUMN target_cohort_revision,
    DROP COLUMN target_cohort_id,
    DROP COLUMN preview_digest,
    DROP COLUMN action_payload,
    ADD CONSTRAINT admin_people_bulk_jobs_action_kind_check CHECK (action_kind IN ('assign_group','suspend_memberships','reactivate_memberships')),
    ADD CONSTRAINT admin_people_bulk_jobs_check CHECK (
        (action_kind='assign_group' AND group_id IS NOT NULL) OR
        (action_kind<>'assign_group' AND group_id IS NULL)
    );

CREATE OR REPLACE FUNCTION public.reject_admin_people_bulk_action_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'admin people bulk action is immutable';
END;
$$;
-- +goose StatementEnd
