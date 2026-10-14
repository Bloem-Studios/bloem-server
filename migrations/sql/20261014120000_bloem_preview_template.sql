-- +goose Up
-- The protected preview template. A group materialized from it makes its
-- accounts preview principals (browse only), so its policy is pinned here as
-- well as in Go: a CHECK on the template revisions and on the materialized
-- groups. IS NOT TRUE / IS NOT DISTINCT FROM keep a NULL from passing a CHECK.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.entitlement_templates WHERE key = 'bloem-preview') THEN
        RAISE EXCEPTION 'entitlement template key "bloem-preview" is reserved for the preview template; rename the existing template and migrate again';
    END IF;
END;
$$;
-- +goose StatementEnd

INSERT INTO public.entitlement_templates (key, name, current_revision, enabled, archived)
VALUES ('bloem-preview', 'Preview', 1, true, false);

INSERT INTO public.entitlement_template_revisions (
    template_key, revision, library_ids, playback_allowed, max_streams,
    max_profiles, transcode_allowed, max_transcodes, download_allowed,
    download_transcode_allowed, max_playback_quality, allowed_permissions,
    requests_allowed
)
VALUES ('bloem-preview', 1, NULL, false, 0, 0, false, 0, false, false, '', '{}', false);

ALTER TABLE public.entitlement_template_revisions
    ADD CONSTRAINT entitlement_template_revisions_preview_invariant CHECK (
        template_key <> 'bloem-preview' OR (
            playback_allowed IS NOT TRUE AND max_streams IS NOT DISTINCT FROM 0 AND
            max_profiles IS NOT DISTINCT FROM 0 AND transcode_allowed IS NOT TRUE AND
            max_transcodes IS NOT DISTINCT FROM 0 AND download_allowed IS NOT TRUE AND
            download_transcode_allowed IS NOT TRUE AND requests_allowed IS NOT TRUE AND
            max_playback_quality IS NOT DISTINCT FROM '' AND
            allowed_permissions IS NOT DISTINCT FROM '{}'::text[]
        )
    );

ALTER TABLE public.access_groups
    ADD CONSTRAINT access_groups_preview_invariant CHECK (
        managed_template_key IS DISTINCT FROM 'bloem-preview' OR (
            playback_allowed IS NOT TRUE AND max_streams IS NOT DISTINCT FROM 0 AND
            max_profiles IS NOT DISTINCT FROM 0 AND transcode_allowed IS NOT TRUE AND
            max_transcodes IS NOT DISTINCT FROM 0 AND download_allowed IS NOT TRUE AND
            download_transcode_allowed IS NOT TRUE AND requests_allowed IS NOT TRUE AND
            max_playback_quality IS NOT DISTINCT FROM '' AND
            allowed_permissions IS NOT DISTINCT FROM '{}'::text[]
        )
    );

-- +goose Down
ALTER TABLE public.access_groups DROP CONSTRAINT IF EXISTS access_groups_preview_invariant;
ALTER TABLE public.entitlement_template_revisions
    DROP CONSTRAINT IF EXISTS entitlement_template_revisions_preview_invariant;
-- The seeded rows stay: template revisions are immutable history, and a
-- materialized preview group may still reference them.
