-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.invitations
    ADD COLUMN organization_id uuid DEFAULT public.vondel_default_organization_id();

UPDATE public.invitations
SET organization_id = public.vondel_default_organization_id()
WHERE organization_id IS NULL;

ALTER TABLE public.invitations
    ALTER COLUMN organization_id SET NOT NULL,
    ADD CONSTRAINT invitations_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES public.organizations(id) ON DELETE CASCADE,
    DROP CONSTRAINT invitations_access_group_id_fkey,
    ADD CONSTRAINT invitations_organization_access_group_fkey
        FOREIGN KEY (organization_id, access_group_id)
        REFERENCES public.access_groups(organization_id, id) ON DELETE SET NULL (access_group_id);

DROP INDEX public.invitations_one_pending_idx;
CREATE UNIQUE INDEX invitations_one_pending_idx
    ON public.invitations (organization_id, email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invitations_organization_created_idx
    ON public.invitations (organization_id, created_at DESC, id DESC);

ALTER TABLE public.policy_decisions
    ADD COLUMN organization_id uuid DEFAULT public.vondel_default_organization_id(),
    ADD COLUMN membership_id uuid;

UPDATE public.policy_decisions
SET organization_id = public.vondel_default_organization_id()
WHERE organization_id IS NULL;

UPDATE public.policy_decisions AS decisions
SET membership_id = memberships.id
FROM public.organization_memberships AS memberships
WHERE memberships.organization_id = decisions.organization_id
  AND memberships.account_id = decisions.user_id
  AND decisions.membership_id IS NULL;

ALTER TABLE public.policy_decisions
    ALTER COLUMN organization_id SET NOT NULL,
    ADD CONSTRAINT policy_decisions_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES public.organizations(id) ON DELETE RESTRICT,
    ADD CONSTRAINT policy_decisions_membership_id_fkey
        FOREIGN KEY (membership_id) REFERENCES public.organization_memberships(id) ON DELETE SET NULL;

CREATE INDEX idx_policy_decisions_organization_timestamp
    ON public.policy_decisions (organization_id, "timestamp" DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_policy_decisions_organization_timestamp;
ALTER TABLE public.policy_decisions
    DROP CONSTRAINT IF EXISTS policy_decisions_membership_id_fkey,
    DROP CONSTRAINT IF EXISTS policy_decisions_organization_id_fkey,
    DROP COLUMN IF EXISTS membership_id,
    DROP COLUMN IF EXISTS organization_id;

DROP INDEX IF EXISTS public.invitations_organization_created_idx;
DROP INDEX IF EXISTS public.invitations_one_pending_idx;

WITH duplicate_pending AS (
    SELECT id, row_number() OVER (PARTITION BY email ORDER BY created_at DESC, id DESC) AS position
    FROM public.invitations
    WHERE accepted_at IS NULL AND revoked_at IS NULL
)
UPDATE public.invitations AS invitations
SET revoked_at = now(), updated_at = now()
FROM duplicate_pending
WHERE invitations.id = duplicate_pending.id
  AND duplicate_pending.position > 1;

CREATE UNIQUE INDEX invitations_one_pending_idx ON public.invitations (email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
ALTER TABLE public.invitations
    DROP CONSTRAINT IF EXISTS invitations_organization_access_group_fkey,
    ADD CONSTRAINT invitations_access_group_id_fkey
        FOREIGN KEY (access_group_id) REFERENCES public.access_groups(id) ON DELETE SET NULL,
    DROP CONSTRAINT IF EXISTS invitations_organization_id_fkey,
    DROP COLUMN IF EXISTS organization_id;
-- +goose StatementEnd
