-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.resource_owners (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (kind IN ('platform', 'organization')),
    organization_id uuid REFERENCES public.organizations(id) ON DELETE RESTRICT,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT resource_owners_kind_organization_check CHECK (
        (kind = 'platform' AND organization_id IS NULL) OR
        (kind = 'organization' AND organization_id IS NOT NULL)
    ),
    CONSTRAINT resource_owners_id_kind_key UNIQUE (id, kind),
    CONSTRAINT resource_owners_id_organization_key UNIQUE (id, organization_id),
    CONSTRAINT resource_owners_organization_key UNIQUE (organization_id)
);

CREATE UNIQUE INDEX resource_owners_one_platform_idx
    ON public.resource_owners(kind)
    WHERE kind = 'platform';

INSERT INTO public.resource_owners (kind)
VALUES ('platform');

INSERT INTO public.resource_owners (kind, organization_id)
SELECT 'organization', id
FROM public.organizations
ORDER BY id;

CREATE FUNCTION public.bloem_create_organization_resource_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO public.resource_owners (kind, organization_id)
    VALUES ('organization', NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER organizations_create_resource_owner
AFTER INSERT ON public.organizations
FOR EACH ROW EXECUTE FUNCTION public.bloem_create_organization_resource_owner();

CREATE FUNCTION public.bloem_platform_resource_owner_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT id FROM public.resource_owners WHERE kind = 'platform'
$$;

ALTER TABLE public.media_folders
    ADD COLUMN owner_id uuid;

ALTER TABLE public.plugin_installations
    ADD COLUMN owner_id uuid;

WITH ordered_roots AS (
    SELECT id
    FROM public.media_folders
    ORDER BY id
)
UPDATE public.media_folders AS roots
SET owner_id = public.bloem_platform_resource_owner_id()
FROM ordered_roots
WHERE roots.id = ordered_roots.id;

WITH ordered_roots AS (
    SELECT id
    FROM public.plugin_installations
    ORDER BY id
)
UPDATE public.plugin_installations AS roots
SET owner_id = public.bloem_platform_resource_owner_id()
FROM ordered_roots
WHERE roots.id = ordered_roots.id;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.media_folders WHERE owner_id IS NULL) THEN
        RAISE EXCEPTION 'resource tenancy left a media folder without an owner';
    END IF;
    IF EXISTS (SELECT 1 FROM public.plugin_installations WHERE owner_id IS NULL) THEN
        RAISE EXCEPTION 'resource tenancy left a plugin installation without an owner';
    END IF;
END;
$$;

ALTER TABLE public.media_folders
    ALTER COLUMN owner_id SET DEFAULT public.bloem_platform_resource_owner_id(),
    ALTER COLUMN owner_id SET NOT NULL,
    ADD CONSTRAINT media_folders_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES public.resource_owners(id) ON DELETE RESTRICT,
    ADD CONSTRAINT media_folders_id_owner_id_key UNIQUE (id, owner_id);

ALTER TABLE public.plugin_installations
    ALTER COLUMN owner_id SET DEFAULT public.bloem_platform_resource_owner_id(),
    ALTER COLUMN owner_id SET NOT NULL,
    ADD CONSTRAINT plugin_installations_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES public.resource_owners(id) ON DELETE RESTRICT,
    ADD CONSTRAINT plugin_installations_id_owner_id_key UNIQUE (id, owner_id);

CREATE INDEX media_folders_owner_id_idx
    ON public.media_folders(owner_id);
CREATE INDEX plugin_installations_owner_id_idx
    ON public.plugin_installations(owner_id);

CREATE TABLE public.entitlement_bundles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'suspended', 'retired')),
    active_revision bigint NOT NULL DEFAULT 1 CHECK (active_revision > 0),
    is_organization_creation_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlement_bundles_id_active_revision_key UNIQUE (id, active_revision)
);

CREATE UNIQUE INDEX entitlement_bundles_slug_ci_idx
    ON public.entitlement_bundles(lower(slug));
CREATE UNIQUE INDEX entitlement_bundles_one_creation_default_idx
    ON public.entitlement_bundles(is_organization_creation_default)
    WHERE is_organization_creation_default;

CREATE TABLE public.entitlement_bundle_versions (
    bundle_id uuid NOT NULL REFERENCES public.entitlement_bundles(id) ON DELETE RESTRICT,
    revision bigint NOT NULL CHECK (revision > 0),
    created_by_account_id integer REFERENCES public.users(id) ON DELETE RESTRICT,
    created_by_service text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bundle_id, revision),
    CONSTRAINT entitlement_bundle_versions_actor_check CHECK (
        (created_by_account_id IS NOT NULL)::integer +
        (created_by_service IS NOT NULL AND btrim(created_by_service) <> '')::integer = 1
    )
);

WITH default_bundle AS (
    INSERT INTO public.entitlement_bundles (
        slug,
        name,
        status,
        active_revision,
        is_organization_creation_default
    ) VALUES (
        'default-platform-catalog',
        'Default Platform Catalog',
        'active',
        1,
        true
    )
    RETURNING id, active_revision
)
INSERT INTO public.entitlement_bundle_versions (
    bundle_id,
    revision,
    created_by_service
)
SELECT id, active_revision, 'resource-tenancy-migration'
FROM default_bundle;

ALTER TABLE public.entitlement_bundles
    ADD CONSTRAINT entitlement_bundles_active_version_fkey
    FOREIGN KEY (id, active_revision)
    REFERENCES public.entitlement_bundle_versions(bundle_id, revision)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE public.entitlement_bundle_members (
    bundle_id uuid NOT NULL,
    bundle_revision bigint NOT NULL,
    entitlement_kind text NOT NULL CHECK (entitlement_kind IN ('library_access', 'plugin_availability')),
    root_kind text NOT NULL CHECK (root_kind IN ('media_folder', 'plugin_installation')),
    root_owner_id uuid NOT NULL,
    root_owner_kind text NOT NULL DEFAULT 'platform' CHECK (root_owner_kind = 'platform'),
    media_folder_id integer,
    plugin_installation_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlement_bundle_members_bundle_version_fkey
        FOREIGN KEY (bundle_id, bundle_revision)
        REFERENCES public.entitlement_bundle_versions(bundle_id, revision) ON DELETE RESTRICT,
    CONSTRAINT entitlement_bundle_members_owner_fkey
        FOREIGN KEY (root_owner_id, root_owner_kind)
        REFERENCES public.resource_owners(id, kind) ON DELETE RESTRICT,
    CONSTRAINT entitlement_bundle_members_media_folder_fkey
        FOREIGN KEY (media_folder_id, root_owner_id)
        REFERENCES public.media_folders(id, owner_id) ON DELETE RESTRICT,
    CONSTRAINT entitlement_bundle_members_plugin_installation_fkey
        FOREIGN KEY (plugin_installation_id, root_owner_id)
        REFERENCES public.plugin_installations(id, owner_id) ON DELETE RESTRICT,
    CONSTRAINT entitlement_bundle_members_typed_root_check CHECK (
        (root_kind = 'media_folder' AND entitlement_kind = 'library_access' AND media_folder_id IS NOT NULL AND plugin_installation_id IS NULL) OR
        (root_kind = 'plugin_installation' AND entitlement_kind = 'plugin_availability' AND media_folder_id IS NULL AND plugin_installation_id IS NOT NULL)
    ),
    CONSTRAINT entitlement_bundle_members_root_key
        UNIQUE NULLS NOT DISTINCT (bundle_id, bundle_revision, media_folder_id, plugin_installation_id)
);

CREATE TABLE public.organization_entitlements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    entitlement_kind text NOT NULL CHECK (entitlement_kind IN ('library_access', 'plugin_availability')),
    root_kind text NOT NULL CHECK (root_kind IN ('media_folder', 'plugin_installation')),
    root_owner_id uuid NOT NULL,
    root_owner_kind text NOT NULL DEFAULT 'platform' CHECK (root_owner_kind = 'platform'),
    media_folder_id integer,
    plugin_installation_id bigint,
    status text NOT NULL CHECK (status IN ('active', 'suspended', 'revoked')),
    source_bundle_id uuid,
    source_bundle_revision bigint,
    security_revision bigint NOT NULL DEFAULT 1 CHECK (security_revision > 0),
    granted_by_account_id integer REFERENCES public.users(id) ON DELETE RESTRICT,
    granted_by_service text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT organization_entitlements_source_pair_check CHECK (
        (source_bundle_id IS NULL AND source_bundle_revision IS NULL) OR
        (source_bundle_id IS NOT NULL AND source_bundle_revision IS NOT NULL)
    ),
    CONSTRAINT organization_entitlements_actor_check CHECK (
        (granted_by_account_id IS NOT NULL)::integer +
        (granted_by_service IS NOT NULL AND btrim(granted_by_service) <> '')::integer = 1
    ),
    CONSTRAINT organization_entitlements_revocation_check CHECK (
        (status = 'revoked' AND revoked_at IS NOT NULL) OR
        (status <> 'revoked' AND revoked_at IS NULL)
    ),
    CONSTRAINT organization_entitlements_source_bundle_fkey
        FOREIGN KEY (source_bundle_id, source_bundle_revision)
        REFERENCES public.entitlement_bundle_versions(bundle_id, revision) ON DELETE RESTRICT,
    CONSTRAINT organization_entitlements_owner_fkey
        FOREIGN KEY (root_owner_id, root_owner_kind)
        REFERENCES public.resource_owners(id, kind) ON DELETE RESTRICT,
    CONSTRAINT organization_entitlements_media_folder_fkey
        FOREIGN KEY (media_folder_id, root_owner_id)
        REFERENCES public.media_folders(id, owner_id) ON DELETE RESTRICT,
    CONSTRAINT organization_entitlements_plugin_installation_fkey
        FOREIGN KEY (plugin_installation_id, root_owner_id)
        REFERENCES public.plugin_installations(id, owner_id) ON DELETE RESTRICT,
    CONSTRAINT organization_entitlements_typed_root_check CHECK (
        (root_kind = 'media_folder' AND entitlement_kind = 'library_access' AND media_folder_id IS NOT NULL AND plugin_installation_id IS NULL) OR
        (root_kind = 'plugin_installation' AND entitlement_kind = 'plugin_availability' AND media_folder_id IS NULL AND plugin_installation_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX organization_entitlements_live_media_folder_idx
    ON public.organization_entitlements(organization_id, media_folder_id)
    WHERE status IN ('active', 'suspended') AND media_folder_id IS NOT NULL;
CREATE UNIQUE INDEX organization_entitlements_live_plugin_installation_idx
    ON public.organization_entitlements(organization_id, plugin_installation_id)
    WHERE status IN ('active', 'suspended') AND plugin_installation_id IS NOT NULL;
CREATE INDEX organization_entitlements_organization_status_idx
    ON public.organization_entitlements(organization_id, status);
CREATE INDEX organization_entitlements_root_owner_idx
    ON public.organization_entitlements(root_owner_id);

CREATE TABLE public.resource_tenancy_migration_ledger (
    phase text NOT NULL,
    root_kind text NOT NULL CHECK (root_kind IN ('media_folder', 'plugin_installation')),
    root_id bigint NOT NULL,
    root_owner_id uuid NOT NULL REFERENCES public.resource_owners(id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('pending', 'complete', 'quarantined')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    diagnostic text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (phase, root_kind, root_id)
);

INSERT INTO public.entitlement_bundle_members (
    bundle_id,
    bundle_revision,
    entitlement_kind,
    root_kind,
    root_owner_id,
    media_folder_id
)
SELECT bundles.id, bundles.active_revision, 'library_access', 'media_folder', folders.owner_id, folders.id
FROM public.entitlement_bundles AS bundles
CROSS JOIN public.media_folders AS folders
WHERE bundles.is_organization_creation_default
ORDER BY folders.id;

INSERT INTO public.entitlement_bundle_members (
    bundle_id,
    bundle_revision,
    entitlement_kind,
    root_kind,
    root_owner_id,
    plugin_installation_id
)
SELECT bundles.id, bundles.active_revision, 'plugin_availability', 'plugin_installation', plugins.owner_id, plugins.id
FROM public.entitlement_bundles AS bundles
CROSS JOIN public.plugin_installations AS plugins
WHERE bundles.is_organization_creation_default
ORDER BY plugins.id;

INSERT INTO public.organization_entitlements (
    organization_id,
    entitlement_kind,
    root_kind,
    root_owner_id,
    media_folder_id,
    plugin_installation_id,
    status,
    source_bundle_id,
    source_bundle_revision,
    granted_by_service
)
SELECT
    organizations.id,
    members.entitlement_kind,
    members.root_kind,
    members.root_owner_id,
    members.media_folder_id,
    members.plugin_installation_id,
    'active',
    members.bundle_id,
    members.bundle_revision,
    'resource-tenancy-migration'
FROM public.organizations
CROSS JOIN public.entitlement_bundle_members AS members
WHERE organizations.is_default
ORDER BY members.root_kind, COALESCE(members.media_folder_id::bigint, members.plugin_installation_id);

INSERT INTO public.resource_tenancy_migration_ledger (
    phase,
    root_kind,
    root_id,
    root_owner_id,
    status,
    attempt_count,
    started_at,
    completed_at
)
SELECT 'root-backfill', 'media_folder', id, owner_id, 'complete', 1, now(), now()
FROM public.media_folders
ORDER BY id;

INSERT INTO public.resource_tenancy_migration_ledger (
    phase,
    root_kind,
    root_id,
    root_owner_id,
    status,
    attempt_count,
    started_at,
    completed_at
)
SELECT 'root-backfill', 'plugin_installation', id, owner_id, 'complete', 1, now(), now()
FROM public.plugin_installations
ORDER BY id;

DO $$
DECLARE
    root_count bigint;
    member_count bigint;
    entitlement_count bigint;
BEGIN
    SELECT (SELECT count(*) FROM public.media_folders) +
           (SELECT count(*) FROM public.plugin_installations)
    INTO root_count;

    SELECT count(*)
    INTO member_count
    FROM public.entitlement_bundle_members AS members
    JOIN public.entitlement_bundles AS bundles
      ON bundles.id = members.bundle_id
     AND bundles.active_revision = members.bundle_revision
    WHERE bundles.is_organization_creation_default;

    SELECT count(*)
    INTO entitlement_count
    FROM public.organization_entitlements AS entitlements
    JOIN public.organizations AS organizations
      ON organizations.id = entitlements.organization_id
    WHERE organizations.is_default
      AND entitlements.status = 'active';

    IF root_count <> member_count OR root_count <> entitlement_count THEN
        RAISE EXCEPTION 'resource tenancy coverage mismatch: roots %, members %, entitlements %',
            root_count, member_count, entitlement_count;
    END IF;
END;
$$;

CREATE FUNCTION public.bloem_entitle_default_organization_media_folder()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM public.resource_owners
        WHERE id = NEW.owner_id AND kind = 'platform'
    ) THEN
        RETURN NEW;
    END IF;

    INSERT INTO public.organization_entitlements (
        organization_id,
        entitlement_kind,
        root_kind,
        root_owner_id,
        media_folder_id,
        status,
        granted_by_service
    )
    SELECT id, 'library_access', 'media_folder', NEW.owner_id, NEW.id, 'active', 'resource-root-compatibility'
    FROM public.organizations
    WHERE is_default
    ON CONFLICT (organization_id, media_folder_id)
        WHERE status IN ('active', 'suspended') AND media_folder_id IS NOT NULL
        DO NOTHING;

    RETURN NEW;
END;
$$;

CREATE TRIGGER media_folders_entitle_default_organization
AFTER INSERT ON public.media_folders
FOR EACH ROW EXECUTE FUNCTION public.bloem_entitle_default_organization_media_folder();

CREATE FUNCTION public.bloem_entitle_default_organization_plugin_installation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM public.resource_owners
        WHERE id = NEW.owner_id AND kind = 'platform'
    ) THEN
        RETURN NEW;
    END IF;

    INSERT INTO public.organization_entitlements (
        organization_id,
        entitlement_kind,
        root_kind,
        root_owner_id,
        plugin_installation_id,
        status,
        granted_by_service
    )
    SELECT id, 'plugin_availability', 'plugin_installation', NEW.owner_id, NEW.id, 'active', 'resource-root-compatibility'
    FROM public.organizations
    WHERE is_default
    ON CONFLICT (organization_id, plugin_installation_id)
        WHERE status IN ('active', 'suspended') AND plugin_installation_id IS NOT NULL
        DO NOTHING;

    RETURN NEW;
END;
$$;

CREATE TRIGGER plugin_installations_entitle_default_organization
AFTER INSERT ON public.plugin_installations
FOR EACH ROW EXECUTE FUNCTION public.bloem_entitle_default_organization_plugin_installation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS plugin_installations_entitle_default_organization ON public.plugin_installations;
DROP FUNCTION IF EXISTS public.bloem_entitle_default_organization_plugin_installation();
DROP TRIGGER IF EXISTS media_folders_entitle_default_organization ON public.media_folders;
DROP FUNCTION IF EXISTS public.bloem_entitle_default_organization_media_folder();

DROP TABLE IF EXISTS public.resource_tenancy_migration_ledger;
DROP TABLE IF EXISTS public.organization_entitlements;
DROP TABLE IF EXISTS public.entitlement_bundle_members;
ALTER TABLE public.entitlement_bundles
    DROP CONSTRAINT IF EXISTS entitlement_bundles_active_version_fkey;
DROP TABLE IF EXISTS public.entitlement_bundle_versions;
DROP TABLE IF EXISTS public.entitlement_bundles;

DROP INDEX IF EXISTS public.plugin_installations_owner_id_idx;
DROP INDEX IF EXISTS public.media_folders_owner_id_idx;

ALTER TABLE public.plugin_installations
    DROP CONSTRAINT IF EXISTS plugin_installations_id_owner_id_key,
    DROP CONSTRAINT IF EXISTS plugin_installations_owner_id_fkey,
    DROP COLUMN IF EXISTS owner_id;

ALTER TABLE public.media_folders
    DROP CONSTRAINT IF EXISTS media_folders_id_owner_id_key,
    DROP CONSTRAINT IF EXISTS media_folders_owner_id_fkey,
    DROP COLUMN IF EXISTS owner_id;

DROP FUNCTION IF EXISTS public.bloem_platform_resource_owner_id();
DROP TRIGGER IF EXISTS organizations_create_resource_owner ON public.organizations;
DROP FUNCTION IF EXISTS public.bloem_create_organization_resource_owner();
DROP TABLE IF EXISTS public.resource_owners;
-- +goose StatementEnd
