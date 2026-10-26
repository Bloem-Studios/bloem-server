-- +goose Up
-- Deleting a library prunes it from every access group's library_ids
-- (prune_deleted_library_access_references), including a template-managed
-- cohort group. The group's cohort revision is immutable and keeps listing
-- the library, so the coherence checks below, comparing the two lists
-- exactly, refused the delete: a library any cohort group listed could not be
-- deleted. Compare only the libraries that still exist instead. Every policy
-- field, and every library that exists, must still match.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.enforce_access_group_cohort_revision_coherence()
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
		  -- Bloem: only libraries that still exist. Deleting a library prunes
		  -- it from the group but not from the immutable revision.
		  AND public.existing_library_ids(r.library_ids) <@ public.existing_library_ids(current_group.library_ids)
		  AND public.existing_library_ids(r.library_ids) @> public.existing_library_ids(current_group.library_ids)
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.enforce_cohort_revision_access_group_coherence()
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
          -- Bloem: only libraries that still exist, as above.
          AND public.existing_library_ids(g.library_ids) <@ public.existing_library_ids(NEW.library_ids)
          AND public.existing_library_ids(g.library_ids) @> public.existing_library_ids(NEW.library_ids)
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.enforce_access_group_cohort_revision_coherence()
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.enforce_cohort_revision_access_group_coherence()
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
-- +goose StatementEnd
