-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.access_groups
    ADD COLUMN organization_id bigint NOT NULL DEFAULT public.default_organization_id()
        REFERENCES public.organizations(id) ON DELETE RESTRICT,
    ADD CONSTRAINT access_groups_organization_id_id_key UNIQUE (organization_id, id),
    DROP CONSTRAINT access_groups_name_key,
    ADD CONSTRAINT access_groups_organization_name_key UNIQUE (organization_id, name);
DROP INDEX public.access_groups_one_default_idx;
CREATE UNIQUE INDEX access_groups_one_default_idx ON public.access_groups (organization_id) WHERE is_default;

ALTER TABLE public.users
    DROP CONSTRAINT users_access_group_id_fkey,
    ADD CONSTRAINT users_organization_access_group_fkey
        FOREIGN KEY (organization_id, access_group_id)
        REFERENCES public.access_groups (organization_id, id)
        ON DELETE SET NULL (access_group_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.access_groups WHERE organization_id <> public.default_organization_id()) THEN
        RAISE EXCEPTION 'cannot remove group ownership while tenant groups exist';
    END IF;
END $$;
ALTER TABLE public.users
    DROP CONSTRAINT users_organization_access_group_fkey,
    ADD CONSTRAINT users_access_group_id_fkey FOREIGN KEY (access_group_id)
        REFERENCES public.access_groups (id) ON DELETE SET NULL;
DROP INDEX public.access_groups_one_default_idx;
CREATE UNIQUE INDEX access_groups_one_default_idx ON public.access_groups (is_default) WHERE is_default;
ALTER TABLE public.access_groups DROP CONSTRAINT access_groups_organization_name_key,
    ADD CONSTRAINT access_groups_name_key UNIQUE (name), DROP COLUMN organization_id;
-- +goose StatementEnd
