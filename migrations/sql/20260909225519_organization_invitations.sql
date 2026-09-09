-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.invite_codes
    ADD COLUMN organization_id bigint NOT NULL DEFAULT public.default_organization_id()
        REFERENCES public.organizations(id) ON DELETE RESTRICT;
ALTER TABLE public.invitations
    ADD COLUMN organization_id bigint REFERENCES public.organizations(id) ON DELETE RESTRICT;
UPDATE public.invitations SET organization_id=(SELECT id FROM public.organizations
    WHERE slug=CASE WHEN invitations.role='admin' THEN 'platform' ELSE 'default' END);
ALTER TABLE public.invitations ALTER COLUMN organization_id SET NOT NULL,
    ALTER COLUMN organization_id SET DEFAULT public.default_organization_id();

ALTER TABLE public.users ADD CONSTRAINT users_organization_id_id_key UNIQUE (organization_id,id);
ALTER TABLE public.invitations DROP CONSTRAINT invitations_access_group_id_fkey,
    DROP CONSTRAINT invitations_accepted_user_id_fkey,
    ADD CONSTRAINT invitations_organization_group_fkey FOREIGN KEY (organization_id,access_group_id)
        REFERENCES public.access_groups(organization_id,id) ON DELETE SET NULL (access_group_id),
    ADD CONSTRAINT invitations_organization_user_fkey FOREIGN KEY (organization_id,accepted_user_id)
        REFERENCES public.users(organization_id,id) ON DELETE SET NULL (accepted_user_id);
DROP INDEX public.invitations_one_pending_idx;
CREATE UNIQUE INDEX invitations_one_pending_idx ON public.invitations (organization_id,email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invite_codes_organization_idx ON public.invite_codes(organization_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.invite_codes WHERE organization_id <> public.default_organization_id())
    OR EXISTS (SELECT 1 FROM public.invitations WHERE organization_id <> public.default_organization_id()) THEN
        RAISE EXCEPTION 'cannot remove invitation ownership while tenant invitations exist';
    END IF;
END $$;
DROP INDEX public.invitations_one_pending_idx;
CREATE UNIQUE INDEX invitations_one_pending_idx ON public.invitations(email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
ALTER TABLE public.invitations DROP CONSTRAINT invitations_organization_group_fkey,
    DROP CONSTRAINT invitations_organization_user_fkey,
    ADD CONSTRAINT invitations_access_group_id_fkey FOREIGN KEY(access_group_id)
        REFERENCES public.access_groups(id) ON DELETE SET NULL,
    ADD CONSTRAINT invitations_accepted_user_id_fkey FOREIGN KEY(accepted_user_id)
        REFERENCES public.users(id) ON DELETE SET NULL,
    DROP COLUMN organization_id;
ALTER TABLE public.invite_codes DROP COLUMN organization_id;
ALTER TABLE public.users DROP CONSTRAINT users_organization_id_id_key;
-- +goose StatementEnd
