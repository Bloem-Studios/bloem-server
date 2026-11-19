-- +goose Up
-- Native storage instances leave Silo's plugin tables.
--
-- Each native storage source has its own instance: its own process, settings
-- and package. Those instances were rows of plugin_installations, so a plugin
-- with two sources had two rows. Silo now allows one plugin_installations row
-- per plugin (20261118203050, which runs next and deletes duplicates), so the
-- instances move to Bloem's own tables first, under the same ids:
--
--   bloem_storage_installations          the instance (was the marker row)
--   bloem_storage_installation_configs   its settings (from plugin_runtime_configs)
--   bloem_storage_installation_archives  its package (from plugin_archives)
--
-- Settings are copied as they are: their ciphertext is bound to the instance
-- id and key under the plugin_runtime_configs name, which Bloem's codec keeps.
-- Sources and organization entitlements then point at the Bloem instance, and
-- the instances' rows are removed from plugin_installations (with their Silo
-- settings, packages and capability rows).

-- A plugin bundle never lists a native instance; refuse rather than lose one.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.entitlement_bundle_members m
        JOIN public.bloem_storage_installations n ON n.installation_id = m.plugin_installation_id
    ) THEN
        RAISE EXCEPTION 'a plugin bundle lists a native storage installation';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE public.bloem_storage_installations
    DROP CONSTRAINT bloem_storage_installations_installation_id_owner_id_fkey,
    ADD COLUMN plugin_id text,
    ADD COLUMN version text,
    ADD COLUMN install_path text,
    ADD COLUMN enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN runtime_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

UPDATE public.bloem_storage_installations n
SET plugin_id = i.plugin_id,
    version = i.version,
    install_path = i.install_path,
    enabled = i.enabled,
    runtime_generation = i.runtime_generation,
    updated_at = i.updated_at
FROM public.plugin_installations i
WHERE i.id = n.installation_id;

-- Defaults as plugin_installations had them.
ALTER TABLE public.bloem_storage_installations
    ALTER COLUMN owner_id SET DEFAULT public.bloem_platform_resource_owner_id(),
    ALTER COLUMN plugin_id SET NOT NULL,
    ALTER COLUMN version SET NOT NULL,
    ALTER COLUMN install_path SET NOT NULL,
    ADD CONSTRAINT bloem_storage_installations_plugin_id_check CHECK (octet_length(plugin_id) BETWEEN 1 AND 1024),
    ADD CONSTRAINT bloem_storage_installations_runtime_generation_check CHECK (runtime_generation >= 0),
    ADD CONSTRAINT bloem_storage_installations_id_owner_key UNIQUE (installation_id, owner_id);

-- New instance ids start at 1,000,000,000, above any id Silo's plugin
-- installations reach, so an id never names both a Silo installation and a
-- Bloem instance. The moved instances keep their ids, which Silo never reuses.
CREATE SEQUENCE public.bloem_storage_installation_id_seq OWNED BY public.bloem_storage_installations.installation_id;
SELECT setval('public.bloem_storage_installation_id_seq', GREATEST(
    (SELECT COALESCE(max(installation_id), 0) FROM public.bloem_storage_installations),
    999999999));
ALTER TABLE public.bloem_storage_installations
    ALTER COLUMN installation_id SET DEFAULT nextval('public.bloem_storage_installation_id_seq');

CREATE TABLE public.bloem_storage_installation_configs (
    installation_id bigint NOT NULL REFERENCES public.bloem_storage_installations(installation_id) ON DELETE CASCADE,
    config_key text NOT NULL CHECK (octet_length(config_key) BETWEEN 1 AND 1024),
    config_value jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, config_key)
);
INSERT INTO public.bloem_storage_installation_configs (installation_id, config_key, config_value, created_at, updated_at)
SELECT c.plugin_installation_id, c.config_key, c.config_value, c.created_at, c.updated_at
FROM public.plugin_runtime_configs c
JOIN public.bloem_storage_installations n ON n.installation_id = c.plugin_installation_id;

CREATE TABLE public.bloem_storage_installation_archives (
    installation_id bigint PRIMARY KEY REFERENCES public.bloem_storage_installations(installation_id) ON DELETE CASCADE,
    manifest_json bytea NOT NULL,
    checksum text NOT NULL,
    archive_bytes bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO public.bloem_storage_installation_archives (installation_id, manifest_json, checksum, archive_bytes, created_at, updated_at)
SELECT a.plugin_installation_id, a.manifest_json, a.checksum, a.archive_bytes, a.created_at, a.updated_at
FROM public.plugin_archives a
JOIN public.bloem_storage_installations n ON n.installation_id = a.plugin_installation_id;

-- Sources point at the Bloem instance. A source that named anything else
-- (never written by Bloem) is left detached, as a removed installation leaves it.
ALTER TABLE public.bloem_storage_sources
    DROP CONSTRAINT bloem_storage_sources_installation_id_fkey,
    DROP CONSTRAINT bloem_storage_sources_installation_owner_fk;
UPDATE public.bloem_storage_sources s SET installation_id = NULL
WHERE s.installation_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM public.bloem_storage_installations n
    WHERE n.installation_id = s.installation_id AND n.owner_id = s.owner_id);
ALTER TABLE public.bloem_storage_sources
    ADD CONSTRAINT bloem_storage_sources_installation_owner_fk FOREIGN KEY (installation_id, owner_id)
        REFERENCES public.bloem_storage_installations(installation_id, owner_id) ON DELETE SET NULL (installation_id);

-- An organization's access to a platform instance becomes a storage
-- entitlement on the instance.
ALTER TABLE public.organization_entitlements
    ADD COLUMN storage_installation_id bigint,
    DROP CONSTRAINT organization_entitlements_root_kind_check,
    DROP CONSTRAINT organization_entitlements_typed_root_check;
UPDATE public.organization_entitlements e
SET storage_installation_id = e.plugin_installation_id,
    plugin_installation_id = NULL,
    root_kind = 'storage_installation',
    updated_at = now()
FROM public.bloem_storage_installations n
WHERE n.installation_id = e.plugin_installation_id;
ALTER TABLE public.organization_entitlements
    ADD CONSTRAINT organization_entitlements_root_kind_check
        CHECK (root_kind IN ('media_folder', 'plugin_installation', 'storage_installation')),
    ADD CONSTRAINT organization_entitlements_typed_root_check CHECK (
        (root_kind = 'media_folder' AND entitlement_kind = 'library_access' AND media_folder_id IS NOT NULL
            AND plugin_installation_id IS NULL AND storage_installation_id IS NULL)
        OR (root_kind = 'plugin_installation' AND entitlement_kind = 'plugin_availability' AND media_folder_id IS NULL
            AND plugin_installation_id IS NOT NULL AND storage_installation_id IS NULL)
        OR (root_kind = 'storage_installation' AND entitlement_kind = 'plugin_availability' AND media_folder_id IS NULL
            AND plugin_installation_id IS NULL AND storage_installation_id IS NOT NULL)),
    ADD CONSTRAINT organization_entitlements_storage_installation_fkey FOREIGN KEY (storage_installation_id, root_owner_id)
        REFERENCES public.bloem_storage_installations(installation_id, owner_id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX organization_entitlements_live_storage_installation_idx
    ON public.organization_entitlements (organization_id, storage_installation_id)
    WHERE status IN ('active', 'suspended') AND storage_installation_id IS NOT NULL;

-- A platform instance is available to the default organization, as a
-- platform plugin installation is.
-- +goose StatementBegin
CREATE FUNCTION public.bloem_entitle_default_organization_storage_installation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM public.resource_owners WHERE id = NEW.owner_id AND kind = 'platform'
    ) THEN
        RETURN NEW;
    END IF;
    INSERT INTO public.organization_entitlements (
        organization_id, entitlement_kind, root_kind, root_owner_id,
        storage_installation_id, status, granted_by_service
    )
    SELECT id, 'plugin_availability', 'storage_installation', NEW.owner_id, NEW.installation_id, 'active', 'resource-root-compatibility'
    FROM public.organizations
    WHERE is_default
    ON CONFLICT (organization_id, storage_installation_id)
        WHERE status IN ('active', 'suspended') AND storage_installation_id IS NOT NULL
        DO NOTHING;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bloem_storage_installations_entitle_default_organization
AFTER INSERT ON public.bloem_storage_installations
FOR EACH ROW EXECUTE FUNCTION public.bloem_entitle_default_organization_storage_installation();

-- The instances are no longer Silo plugin installations. Their Silo settings,
-- packages and capability rows go with them (ON DELETE CASCADE).
DELETE FROM public.plugin_installations i
USING public.bloem_storage_installations n
WHERE i.id = n.installation_id;

-- +goose Down
-- Put the instances back in plugin_installations under their ids.
INSERT INTO public.plugin_installations (id, plugin_id, version, install_path, enabled, update_policy, owner_id, runtime_generation, created_at, updated_at)
OVERRIDING SYSTEM VALUE
SELECT n.installation_id, n.plugin_id, n.version, n.install_path, n.enabled, 'manual', n.owner_id, n.runtime_generation, n.created_at, n.updated_at
FROM public.bloem_storage_installations n;
-- The insert trigger above entitled the default organization again; the
-- moved entitlements below are the ones to keep.
DELETE FROM public.organization_entitlements e
USING public.bloem_storage_installations n
WHERE e.plugin_installation_id = n.installation_id
  AND e.granted_by_service = 'resource-root-compatibility'
  AND EXISTS (SELECT 1 FROM public.organization_entitlements m
              WHERE m.storage_installation_id = n.installation_id AND m.organization_id = e.organization_id
                AND m.status IN ('active', 'suspended'));
INSERT INTO public.plugin_runtime_configs (plugin_installation_id, config_key, config_value, created_at, updated_at)
SELECT installation_id, config_key, config_value, created_at, updated_at FROM public.bloem_storage_installation_configs;
INSERT INTO public.plugin_archives (plugin_installation_id, manifest_json, checksum, archive_bytes, created_at, updated_at)
SELECT installation_id, manifest_json, checksum, archive_bytes, created_at, updated_at FROM public.bloem_storage_installation_archives;

DROP TRIGGER bloem_storage_installations_entitle_default_organization ON public.bloem_storage_installations;
DROP FUNCTION public.bloem_entitle_default_organization_storage_installation();

DROP INDEX public.organization_entitlements_live_storage_installation_idx;
ALTER TABLE public.organization_entitlements
    DROP CONSTRAINT organization_entitlements_storage_installation_fkey,
    DROP CONSTRAINT organization_entitlements_typed_root_check,
    DROP CONSTRAINT organization_entitlements_root_kind_check;
UPDATE public.organization_entitlements
SET plugin_installation_id = storage_installation_id, storage_installation_id = NULL, root_kind = 'plugin_installation'
WHERE storage_installation_id IS NOT NULL;
ALTER TABLE public.organization_entitlements
    DROP COLUMN storage_installation_id,
    ADD CONSTRAINT organization_entitlements_root_kind_check CHECK (root_kind IN ('media_folder', 'plugin_installation')),
    ADD CONSTRAINT organization_entitlements_typed_root_check CHECK (
        (root_kind = 'media_folder' AND entitlement_kind = 'library_access' AND media_folder_id IS NOT NULL AND plugin_installation_id IS NULL)
        OR (root_kind = 'plugin_installation' AND entitlement_kind = 'plugin_availability' AND media_folder_id IS NULL AND plugin_installation_id IS NOT NULL));

ALTER TABLE public.bloem_storage_sources DROP CONSTRAINT bloem_storage_sources_installation_owner_fk;
ALTER TABLE public.bloem_storage_sources
    ADD CONSTRAINT bloem_storage_sources_installation_id_fkey FOREIGN KEY (installation_id)
        REFERENCES public.plugin_installations(id) ON DELETE SET NULL,
    ADD CONSTRAINT bloem_storage_sources_installation_owner_fk FOREIGN KEY (installation_id, owner_id)
        REFERENCES public.plugin_installations(id, owner_id) ON DELETE SET NULL (installation_id);

DROP TABLE public.bloem_storage_installation_archives;
DROP TABLE public.bloem_storage_installation_configs;

ALTER TABLE public.bloem_storage_installations
    ALTER COLUMN installation_id DROP DEFAULT;
DROP SEQUENCE public.bloem_storage_installation_id_seq;
ALTER TABLE public.bloem_storage_installations
    ALTER COLUMN owner_id DROP DEFAULT,
    DROP CONSTRAINT bloem_storage_installations_id_owner_key,
    DROP CONSTRAINT bloem_storage_installations_runtime_generation_check,
    DROP CONSTRAINT bloem_storage_installations_plugin_id_check,
    DROP COLUMN updated_at,
    DROP COLUMN runtime_generation,
    DROP COLUMN enabled,
    DROP COLUMN install_path,
    DROP COLUMN version,
    DROP COLUMN plugin_id,
    ADD CONSTRAINT bloem_storage_installations_installation_id_owner_id_fkey FOREIGN KEY (installation_id, owner_id)
        REFERENCES public.plugin_installations(id, owner_id) ON DELETE CASCADE;
