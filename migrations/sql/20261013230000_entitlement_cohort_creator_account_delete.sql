-- +goose Up
-- A cohort revision records the account that created it, and that foreign key
-- was ON DELETE RESTRICT, so the creator could never be deleted. Direct-account
-- template applies also recorded the target account as the creator, which made
-- the first account provisioned onto each template revision undeletable.
-- Deleting the creator now clears created_by_account_id; the immutability
-- trigger allows exactly that one transition and still refuses every other
-- update and every delete.
ALTER TABLE public.entitlement_policy_cohort_revisions
    ALTER COLUMN created_by_account_id DROP NOT NULL,
    DROP CONSTRAINT entitlement_policy_cohort_revisions_created_by_account_id_fkey,
    ADD CONSTRAINT entitlement_policy_cohort_revisions_created_by_account_id_fkey
        FOREIGN KEY (created_by_account_id) REFERENCES public.users(id) ON DELETE SET NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.reject_entitlement_policy_cohort_revision_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.created_by_account_id IS NOT NULL AND NEW.created_by_account_id IS NULL
       AND (to_jsonb(NEW) - 'created_by_account_id') = (to_jsonb(OLD) - 'created_by_account_id') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'entitlement policy cohort revisions are immutable';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.reject_entitlement_policy_cohort_revision_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'entitlement policy cohort revisions are immutable';
END;
$$;
-- +goose StatementEnd

-- NOT NULL is not restored: deleted creators may already have been cleared.
ALTER TABLE public.entitlement_policy_cohort_revisions
    DROP CONSTRAINT entitlement_policy_cohort_revisions_created_by_account_id_fkey,
    ADD CONSTRAINT entitlement_policy_cohort_revisions_created_by_account_id_fkey
        FOREIGN KEY (created_by_account_id) REFERENCES public.users(id) ON DELETE RESTRICT;
