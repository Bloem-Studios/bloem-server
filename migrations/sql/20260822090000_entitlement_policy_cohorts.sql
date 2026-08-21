-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.entitlement_policy_cohorts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE RESTRICT,
    name text NOT NULL CHECK (name = btrim(name) AND name <> ''),
    archived boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlement_policy_cohorts_id_organization_key
        UNIQUE (id, organization_id)
);

ALTER TABLE public.access_groups
    ADD COLUMN managed_cohort_id uuid,
    ADD CONSTRAINT access_groups_managed_cohort_requires_template
        CHECK (managed_cohort_id IS NULL OR managed_template_key IS NOT NULL);

DROP INDEX public.access_groups_managed_template_revision_per_organization_idx;
CREATE UNIQUE INDEX access_groups_unadopted_template_revision_per_organization_idx
    ON public.access_groups (organization_id, managed_template_key, managed_template_revision)
    WHERE managed_template_key IS NOT NULL
      AND managed_cohort_id IS NULL
      AND NOT is_default;

CREATE TABLE public.entitlement_policy_cohort_revisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cohort_id uuid NOT NULL,
    organization_id uuid NOT NULL,
    name text NOT NULL CHECK (name = btrim(name) AND name <> ''),
    revision bigint NOT NULL CHECK (revision > 0),
    access_group_id bigint NOT NULL,
    source_template_key text NOT NULL,
    source_template_revision bigint NOT NULL CHECK (source_template_revision > 0),
    parent_id uuid,
    derivation_kind text NOT NULL
        CHECK (derivation_kind IN ('exact_template', 'policy_patch', 'managed_default')),
    library_ids integer[] NOT NULL,
    playback_allowed boolean NOT NULL,
    max_streams integer NOT NULL CHECK (max_streams >= 0),
    max_profiles integer NOT NULL CHECK (max_profiles >= 0),
    transcode_allowed boolean NOT NULL,
    max_transcodes integer NOT NULL CHECK (max_transcodes >= 0),
    download_allowed boolean NOT NULL,
    download_transcode_allowed boolean NOT NULL,
    max_playback_quality text NOT NULL DEFAULT '',
    allowed_permissions text[],
    requests_allowed boolean NOT NULL,
    policy_digest text NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    created_by_account_id integer NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlement_policy_cohort_revisions_identity_fkey
        FOREIGN KEY (cohort_id, organization_id)
        REFERENCES public.entitlement_policy_cohorts(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT entitlement_policy_cohort_revisions_group_fkey
        FOREIGN KEY (organization_id, access_group_id)
        REFERENCES public.access_groups(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT entitlement_policy_cohort_revisions_template_fkey
        FOREIGN KEY (source_template_key, source_template_revision)
        REFERENCES public.entitlement_template_revisions(template_key, revision)
        ON DELETE RESTRICT,
    CONSTRAINT entitlement_policy_cohort_revisions_revision_key
        UNIQUE (cohort_id, revision),
    CONSTRAINT entitlement_policy_cohort_revisions_group_key
        UNIQUE (organization_id, access_group_id),
    CONSTRAINT entitlement_policy_cohort_revisions_id_organization_key
        UNIQUE (id, organization_id),
    CONSTRAINT entitlement_policy_cohort_revisions_marker_key
        UNIQUE (
            id,
            organization_id,
            access_group_id,
            source_template_key,
            source_template_revision
        ),
    CONSTRAINT entitlement_policy_cohort_revisions_derivation
        CHECK (
            (derivation_kind = 'exact_template' AND parent_id IS NULL) OR
            (derivation_kind = 'policy_patch' AND parent_id IS NOT NULL) OR
            (derivation_kind = 'managed_default' AND parent_id IS NULL)
        ),
    CONSTRAINT entitlement_policy_cohort_revisions_no_self_parent
        CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT entitlement_policy_cohort_download_dependency
        CHECK (NOT download_transcode_allowed OR download_allowed),
    CONSTRAINT entitlement_policy_cohort_playback_dependency
        CHECK (playback_allowed OR (
            max_streams = 0 AND
            NOT transcode_allowed AND
            max_transcodes = 0 AND
            NOT download_allowed AND
            NOT download_transcode_allowed
        )),
    CONSTRAINT entitlement_policy_cohort_library_ids_positive
        CHECK (0 < ALL(library_ids))
);

ALTER TABLE public.entitlement_policy_cohort_revisions
    ADD CONSTRAINT entitlement_policy_cohort_revisions_parent_fkey
    FOREIGN KEY (parent_id, organization_id)
    REFERENCES public.entitlement_policy_cohort_revisions(id, organization_id)
    ON DELETE RESTRICT;

ALTER TABLE public.access_groups
    ADD CONSTRAINT access_groups_managed_cohort_source_fkey
    FOREIGN KEY (
        managed_cohort_id,
        organization_id,
        id,
        managed_template_key,
        managed_template_revision
    )
    REFERENCES public.entitlement_policy_cohort_revisions (
        id,
        organization_id,
        access_group_id,
        source_template_key,
        source_template_revision
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX access_groups_managed_cohort_revision_idx
    ON public.access_groups (managed_cohort_id)
    WHERE managed_cohort_id IS NOT NULL;

CREATE FUNCTION public.normalize_entitlement_policy_playback_quality(value text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE upper(btrim(COALESCE(value, '')))
        WHEN '' THEN ''
        WHEN 'ANY' THEN ''
        WHEN 'STANDARD' THEN '1080p'
        WHEN '480P' THEN '1080p'
        WHEN '720P' THEN '1080p'
        WHEN '1080P' THEN '1080p'
        WHEN '4K' THEN '2160p'
        WHEN 'UHD' THEN '2160p'
        WHEN '2160P' THEN '2160p'
        WHEN '4320P' THEN '2160p'
        ELSE ''
    END
$$;

CREATE FUNCTION public.enforce_access_group_cohort_revision_coherence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_group public.access_groups%ROWTYPE;
BEGIN
	-- A deferred row trigger may have multiple queued versions of the same
	-- group while an existing materialization is adopted. Validate the final
	-- row, not the historical NEW tuple from an earlier statement.
	SELECT * INTO current_group
	FROM public.access_groups
	WHERE id = NEW.id;

	IF current_group.managed_cohort_id IS NULL THEN
        IF EXISTS (
            SELECT 1
            FROM public.entitlement_policy_cohort_revisions r
			WHERE r.organization_id = current_group.organization_id
			  AND r.access_group_id = current_group.id
        ) THEN
            RAISE EXCEPTION 'cohort revision access group must retain its managed marker'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM public.entitlement_policy_cohort_revisions r
		WHERE r.id = current_group.managed_cohort_id
		  AND r.organization_id = current_group.organization_id
		  AND r.access_group_id = current_group.id
		  AND r.source_template_key = current_group.managed_template_key
		  AND r.source_template_revision = current_group.managed_template_revision
		  AND r.library_ids <@ current_group.library_ids
		  AND r.library_ids @> current_group.library_ids
		  AND r.playback_allowed IS NOT DISTINCT FROM current_group.playback_allowed
		  AND r.max_streams IS NOT DISTINCT FROM current_group.max_streams
		  AND r.max_profiles IS NOT DISTINCT FROM current_group.max_profiles
		  AND r.transcode_allowed IS NOT DISTINCT FROM current_group.transcode_allowed
		  AND r.max_transcodes IS NOT DISTINCT FROM current_group.max_transcodes
		  AND r.download_allowed IS NOT DISTINCT FROM current_group.download_allowed
		  AND r.download_transcode_allowed IS NOT DISTINCT FROM current_group.download_transcode_allowed
          AND public.normalize_entitlement_policy_playback_quality(r.max_playback_quality) =
			  public.normalize_entitlement_policy_playback_quality(current_group.max_playback_quality)
          AND (
			  (r.allowed_permissions IS NULL AND current_group.allowed_permissions IS NULL) OR
              (
                  r.allowed_permissions IS NOT NULL AND
				  current_group.allowed_permissions IS NOT NULL AND
				  r.allowed_permissions <@ current_group.allowed_permissions AND
				  r.allowed_permissions @> current_group.allowed_permissions
              )
          )
		  AND r.requests_allowed IS NOT DISTINCT FROM current_group.requests_allowed
    ) THEN
        RAISE EXCEPTION 'managed cohort marker must match its immutable revision and policy'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER access_groups_managed_cohort_coherence
AFTER INSERT OR UPDATE OF
    managed_cohort_id,
    organization_id,
    managed_template_key,
    managed_template_revision,
    library_ids,
    playback_allowed,
    max_streams,
    max_profiles,
    transcode_allowed,
    max_transcodes,
    download_allowed,
    download_transcode_allowed,
    max_playback_quality,
    allowed_permissions,
    requests_allowed
ON public.access_groups
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.enforce_access_group_cohort_revision_coherence();

CREATE FUNCTION public.enforce_cohort_revision_access_group_coherence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM public.access_groups g
        WHERE g.organization_id = NEW.organization_id
          AND g.id = NEW.access_group_id
          AND g.managed_cohort_id = NEW.id
          AND g.managed_template_key = NEW.source_template_key
          AND g.managed_template_revision = NEW.source_template_revision
          AND g.library_ids <@ NEW.library_ids
          AND g.library_ids @> NEW.library_ids
          AND g.playback_allowed IS NOT DISTINCT FROM NEW.playback_allowed
          AND g.max_streams IS NOT DISTINCT FROM NEW.max_streams
          AND g.max_profiles IS NOT DISTINCT FROM NEW.max_profiles
          AND g.transcode_allowed IS NOT DISTINCT FROM NEW.transcode_allowed
          AND g.max_transcodes IS NOT DISTINCT FROM NEW.max_transcodes
          AND g.download_allowed IS NOT DISTINCT FROM NEW.download_allowed
          AND g.download_transcode_allowed IS NOT DISTINCT FROM NEW.download_transcode_allowed
          AND public.normalize_entitlement_policy_playback_quality(g.max_playback_quality) =
              public.normalize_entitlement_policy_playback_quality(NEW.max_playback_quality)
          AND (
              (g.allowed_permissions IS NULL AND NEW.allowed_permissions IS NULL) OR
              (
                  g.allowed_permissions IS NOT NULL AND
                  NEW.allowed_permissions IS NOT NULL AND
                  g.allowed_permissions <@ NEW.allowed_permissions AND
                  g.allowed_permissions @> NEW.allowed_permissions
              )
          )
          AND g.requests_allowed IS NOT DISTINCT FROM NEW.requests_allowed
    ) THEN
        RAISE EXCEPTION 'cohort revision must have one matching managed access group'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER entitlement_policy_cohort_revision_group_coherence
AFTER INSERT ON public.entitlement_policy_cohort_revisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.enforce_cohort_revision_access_group_coherence();

CREATE UNIQUE INDEX entitlement_policy_cohort_exact_template_idx
    ON public.entitlement_policy_cohort_revisions (
        organization_id,
        source_template_key,
        source_template_revision
    )
    WHERE derivation_kind = 'exact_template';

CREATE UNIQUE INDEX entitlement_policy_cohort_derived_convergence_idx
    ON public.entitlement_policy_cohort_revisions (
        organization_id,
        parent_id,
        lower(name),
        policy_digest
    )
    WHERE derivation_kind = 'policy_patch';

CREATE INDEX entitlement_policy_cohort_revisions_organization_created_idx
    ON public.entitlement_policy_cohort_revisions (organization_id, created_at DESC, id DESC);

CREATE FUNCTION public.reject_entitlement_policy_cohort_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'entitlement policy cohort revisions are immutable';
END;
$$;

CREATE TRIGGER entitlement_policy_cohort_revisions_immutable
BEFORE UPDATE OR DELETE ON public.entitlement_policy_cohort_revisions
FOR EACH ROW EXECUTE FUNCTION public.reject_entitlement_policy_cohort_revision_mutation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.entitlement_policy_cohort_revisions
        WHERE derivation_kind <> 'exact_template'
    ) THEN
        RAISE EXCEPTION 'cannot roll back: derived entitlement policy cohorts exist';
    END IF;
END;
$$;

DROP INDEX IF EXISTS public.access_groups_unadopted_template_revision_per_organization_idx;

DROP TRIGGER IF EXISTS entitlement_policy_cohort_revision_group_coherence
    ON public.entitlement_policy_cohort_revisions;
DROP FUNCTION IF EXISTS public.enforce_cohort_revision_access_group_coherence();
DROP TRIGGER IF EXISTS access_groups_managed_cohort_coherence
    ON public.access_groups;
DROP FUNCTION IF EXISTS public.enforce_access_group_cohort_revision_coherence();
DROP FUNCTION IF EXISTS public.normalize_entitlement_policy_playback_quality(text);
DROP INDEX IF EXISTS public.access_groups_managed_cohort_revision_idx;

ALTER TABLE public.access_groups
    DROP CONSTRAINT IF EXISTS access_groups_managed_cohort_source_fkey,
    DROP CONSTRAINT IF EXISTS access_groups_managed_cohort_requires_template,
    DROP COLUMN IF EXISTS managed_cohort_id;

CREATE UNIQUE INDEX access_groups_managed_template_revision_per_organization_idx
    ON public.access_groups (organization_id, managed_template_key, managed_template_revision)
    WHERE managed_template_key IS NOT NULL AND NOT is_default;

DROP TRIGGER IF EXISTS entitlement_policy_cohort_revisions_immutable
    ON public.entitlement_policy_cohort_revisions;
DROP FUNCTION IF EXISTS public.reject_entitlement_policy_cohort_revision_mutation();
DROP TABLE IF EXISTS public.entitlement_policy_cohort_revisions;
DROP TABLE IF EXISTS public.entitlement_policy_cohorts;
-- +goose StatementEnd
