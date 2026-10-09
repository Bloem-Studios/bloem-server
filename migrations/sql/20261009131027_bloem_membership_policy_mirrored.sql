-- +goose Up
-- +goose StatementBegin
-- Bloem's membership authority gains a third phase so one database can be served
-- by upstream Silo and by Bloem, one at a time, without a policy freeze:
-- 'mirrored' keeps the legacy users policy columns (Silo's) and the default
-- organization membership (Bloem's) identical, copying a write on either side
-- to the other in the same transaction. It is single-organization by
-- construction, refuses what Silo could not honor, and is entered only by the
-- operator command `silo membership-policy enable-silo-switching`.
ALTER TABLE public.membership_policy_authority
    DROP CONSTRAINT membership_policy_authority_phase_check,
    DROP CONSTRAINT membership_policy_authority_check,
    ADD COLUMN mirrored_at timestamptz,
    ADD CONSTRAINT membership_policy_authority_phase_check
        CHECK (phase IN ('compatibility', 'mirrored', 'finalized')),
    ADD CONSTRAINT membership_policy_authority_check CHECK (
        (phase = 'compatibility' AND mirrored_at IS NULL AND finalized_at IS NULL)
        OR (phase = 'mirrored' AND mirrored_at IS NOT NULL AND finalized_at IS NULL)
        OR (phase = 'finalized' AND finalized_at IS NOT NULL)
    );

-- The value a Bloem policy writer sets bloem.membership_policy_writer to: 'v1'
-- once policy writes are open (mirrored or finalized), '' while frozen.
CREATE FUNCTION public.bloem_membership_policy_writer_marker()
RETURNS text
LANGUAGE sql
STABLE
AS $$
    SELECT CASE WHEN phase IN ('mirrored', 'finalized') THEN 'v1' ELSE '' END
    FROM public.membership_policy_authority WHERE singleton
$$;

CREATE OR REPLACE FUNCTION public.fence_legacy_user_policy_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    -- Mirrored: users is Silo's policy authority and is copied to the
    -- default-organization membership by users_policy_mirror_to_membership.
    IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'mirrored' THEN
        RETURN NEW;
    END IF;
    IF NEW.access_group_id IS DISTINCT FROM OLD.access_group_id
       OR NEW.permissions IS DISTINCT FROM OLD.permissions
       OR NEW.library_ids IS DISTINCT FROM OLD.library_ids
       OR NEW.max_playback_quality IS DISTINCT FROM OLD.max_playback_quality
       OR NEW.max_streams IS DISTINCT FROM OLD.max_streams
       OR NEW.max_transcodes IS DISTINCT FROM OLD.max_transcodes
       OR NEW.transcode_allowed IS DISTINCT FROM OLD.transcode_allowed
       OR NEW.audio_transcode_allowed IS DISTINCT FROM OLD.audio_transcode_allowed
       OR NEW.download_allowed IS DISTINCT FROM OLD.download_allowed
       OR NEW.download_transcode_allowed IS DISTINCT FROM OLD.download_transcode_allowed
       OR NEW.requests_allowed IS DISTINCT FROM OLD.requests_allowed
       OR NEW.max_profiles IS DISTINCT FROM OLD.max_profiles
       OR NEW.access_policy_revision IS DISTINCT FROM OLD.access_policy_revision THEN
        RAISE EXCEPTION 'membership_policy_fenced' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.seed_legacy_membership_policy()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    marked boolean := current_setting('bloem.membership_policy_writer', true) = 'v1';
    account public.users%ROWTYPE;
    account_group_organization uuid;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;
    IF marked AND authority_phase = 'compatibility' THEN
        RAISE EXCEPTION 'membership_policy_not_finalized' USING ERRCODE = 'P0001';
    ELSIF NOT marked AND authority_phase = 'finalized' THEN
        RAISE EXCEPTION 'membership_policy_finalized' USING ERRCODE = 'P0001';
    ELSIF marked THEN
        RETURN NEW;
    END IF;

    SELECT * INTO STRICT account FROM public.users WHERE id = NEW.account_id;
    SELECT organization_id INTO account_group_organization
    FROM public.access_groups WHERE id = account.access_group_id;
    NEW.access_group_id := CASE
        WHEN account_group_organization = NEW.organization_id THEN account.access_group_id
        ELSE (SELECT id FROM public.access_groups WHERE organization_id = NEW.organization_id AND is_default)
    END;
    IF NEW.status = 'active' AND account.role <> 'admin' AND NEW.access_group_id IS NULL THEN
        RAISE EXCEPTION 'membership_policy_missing_organization_group' USING ERRCODE = 'P0001';
    END IF;
    NEW.permissions := account.permissions;
    NEW.library_ids := account.library_ids;
    NEW.max_playback_quality := account.max_playback_quality;
    NEW.max_streams := account.max_streams;
    NEW.max_transcodes := account.max_transcodes;
    NEW.transcode_allowed := account.transcode_allowed;
    NEW.audio_transcode_allowed := account.audio_transcode_allowed;
    NEW.download_allowed := account.download_allowed;
    NEW.download_transcode_allowed := account.download_transcode_allowed;
    NEW.requests_allowed := account.requests_allowed;
    NEW.max_profiles := account.max_profiles;
    NEW.access_policy_revision := account.access_policy_revision;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_membership_policy_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;
    IF current_setting('bloem.membership_policy_writer', true) <> 'v1'
       OR authority_phase NOT IN ('mirrored', 'finalized') THEN
        RAISE EXCEPTION 'membership_policy_not_finalized' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_membership_policy_authority_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'membership_policy_authority_immutable' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.singleton IS DISTINCT FROM OLD.singleton
       OR NEW.fenced_at IS DISTINCT FROM OLD.fenced_at THEN
        RAISE EXCEPTION 'membership_policy_authority_immutable' USING ERRCODE = 'P0001';
    END IF;
    -- Silo switching: only the operator command sets the enabler marker.
    IF OLD.phase = 'compatibility' AND NEW.phase = 'mirrored'
       AND NEW.mirrored_at IS NOT NULL AND NEW.finalized_at IS NULL
       AND current_setting('bloem.membership_policy_mirror_enabler', true) = 'v1' THEN
        RETURN NEW;
    END IF;
    -- Finalizing, from compatibility or from mirrored (which ends switching).
    IF OLD.phase IN ('compatibility', 'mirrored') AND NEW.phase = 'finalized'
       AND NEW.finalized_at IS NOT NULL AND NEW.finalized_at >= OLD.fenced_at
       AND NEW.mirrored_at IS NOT DISTINCT FROM OLD.mirrored_at
       AND current_setting('bloem.membership_policy_finalizer', true) = 'v1' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'membership_policy_authority_immutable' USING ERRCODE = 'P0001';
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_membership_policy_rollout_observation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    current_observation bigint;
	current_heartbeat_at timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'membership_policy_observation_immutable' USING ERRCODE = 'P0001';
    END IF;
    IF current_setting('bloem.membership_policy_observation_writer', true) = 'heartbeat_v1' THEN
        IF TG_OP = 'INSERT' THEN
            RETURN NEW;
        END IF;
        IF NEW.observation_id IS DISTINCT FROM OLD.observation_id
           OR NEW.node_id IS DISTINCT FROM OLD.node_id
           OR NEW.node_type IS DISTINCT FROM OLD.node_type
           OR NEW.state IS DISTINCT FROM OLD.state
           OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
           OR NEW.observed_at IS DISTINCT FROM OLD.observed_at
           OR NEW.drained_at IS DISTINCT FROM OLD.drained_at
           OR NEW.last_seen_at < OLD.last_seen_at THEN
            RAISE EXCEPTION 'membership_policy_observation_immutable' USING ERRCODE = 'P0001';
        END IF;
        RETURN NEW;
    END IF;
    IF current_setting('bloem.membership_policy_observation_writer', true) = 'operator_drain_v1'
       AND TG_OP = 'UPDATE'
       AND OLD.state = 'legacy'
       AND NEW.state = 'drained'
       AND NEW.observation_id = OLD.observation_id
       AND NEW.node_id = OLD.node_id
       AND NEW.node_type = OLD.node_type
       AND NEW.instance_id IS NULL
       AND NEW.observed_at = OLD.observed_at
       AND NEW.last_seen_at = OLD.last_seen_at
       AND NEW.drained_at IS NOT NULL
       AND NEW.drained_at >= OLD.last_seen_at THEN
        SELECT phase INTO authority_phase FROM public.membership_policy_authority WHERE singleton;
        SELECT membership_policy_rollout_observation_id, updated_at
		INTO current_observation, current_heartbeat_at
        FROM public.node_heartbeats WHERE node_id = OLD.node_id;
        IF authority_phase IN ('compatibility', 'mirrored')
		   AND (
			current_observation IS DISTINCT FROM OLD.observation_id
			OR current_heartbeat_at < clock_timestamp() - interval '5 minutes'
		   ) THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION 'membership_policy_observation_immutable' USING ERRCODE = 'P0001';
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_node_heartbeat_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    observation_state text;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;
    IF current_setting('bloem.heartbeat_cleanup_writer', true) = 'v1'
       AND current_setting('bloem.heartbeat_cleanup_node_id', true) = OLD.node_id
       AND current_setting('bloem.heartbeat_cleanup_instance_id', true) = OLD.instance_id::text THEN
        RETURN OLD;
    END IF;
    IF authority_phase IN ('compatibility', 'mirrored')
       AND OLD.instance_id IS NULL
       AND OLD.membership_policy_rollout_observation_id IS NOT NULL THEN
        SELECT state INTO observation_state
        FROM public.membership_policy_rollout_observations
        WHERE observation_id = OLD.membership_policy_rollout_observation_id;
        IF observation_state = 'legacy' THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'membership_policy_heartbeat_delete_fenced' USING ERRCODE = 'P0001';
END;
$$;

-- users -> default-organization membership. The mirroring setting stops the
-- reverse trigger from copying the row straight back; both settings are
-- restored so a later write in the same transaction is mirrored normally.
CREATE FUNCTION public.bloem_mirror_user_policy_to_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    previous_mirroring text := current_setting('bloem.membership_policy_mirroring', true);
    previous_writer text := current_setting('bloem.membership_policy_writer', true);
BEGIN
    IF previous_mirroring = 'on'
       OR (SELECT phase FROM public.membership_policy_authority WHERE singleton) <> 'mirrored'
       OR ROW(NEW.access_group_id, NEW.permissions, NEW.library_ids, NEW.max_playback_quality, NEW.max_streams, NEW.max_transcodes, NEW.transcode_allowed, NEW.audio_transcode_allowed, NEW.download_allowed, NEW.download_transcode_allowed, NEW.requests_allowed, NEW.max_profiles, NEW.access_policy_revision)
          IS NOT DISTINCT FROM
          ROW(OLD.access_group_id, OLD.permissions, OLD.library_ids, OLD.max_playback_quality, OLD.max_streams, OLD.max_transcodes, OLD.transcode_allowed, OLD.audio_transcode_allowed, OLD.download_allowed, OLD.download_transcode_allowed, OLD.requests_allowed, OLD.max_profiles, OLD.access_policy_revision) THEN
        RETURN NULL;
    END IF;
    PERFORM set_config('bloem.membership_policy_mirroring', 'on', true);
    PERFORM set_config('bloem.membership_policy_writer', 'v1', true);
    UPDATE public.organization_memberships
    SET access_group_id = NEW.access_group_id,
        permissions = NEW.permissions,
        library_ids = NEW.library_ids,
        max_playback_quality = NEW.max_playback_quality,
        max_streams = NEW.max_streams,
        max_transcodes = NEW.max_transcodes,
        transcode_allowed = NEW.transcode_allowed,
        audio_transcode_allowed = NEW.audio_transcode_allowed,
        download_allowed = NEW.download_allowed,
        download_transcode_allowed = NEW.download_transcode_allowed,
        requests_allowed = NEW.requests_allowed,
        max_profiles = NEW.max_profiles,
        access_policy_revision = NEW.access_policy_revision,
        updated_at = now()
    WHERE account_id = NEW.id
      AND organization_id = public.bloem_default_organization_id();
    PERFORM set_config('bloem.membership_policy_writer', COALESCE(previous_writer, ''), true);
    PERFORM set_config('bloem.membership_policy_mirroring', COALESCE(previous_mirroring, ''), true);
    RETURN NULL;
END;
$$;

CREATE TRIGGER users_policy_mirror_to_membership
AFTER UPDATE OF access_group_id, permissions, library_ids, max_playback_quality, max_streams, max_transcodes, transcode_allowed, audio_transcode_allowed, download_allowed, download_transcode_allowed, requests_allowed, max_profiles, access_policy_revision
ON public.users
FOR EACH ROW EXECUTE FUNCTION public.bloem_mirror_user_policy_to_membership();

-- Default-organization membership -> users. Memberships in other organizations
-- cannot exist while mirrored (bloem_mirrored_guard).
CREATE FUNCTION public.bloem_mirror_membership_policy_to_user()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    previous_mirroring text := current_setting('bloem.membership_policy_mirroring', true);
BEGIN
    IF previous_mirroring = 'on'
       OR (SELECT phase FROM public.membership_policy_authority WHERE singleton) <> 'mirrored'
       OR NEW.organization_id IS DISTINCT FROM public.bloem_default_organization_id() THEN
        RETURN NULL;
    END IF;
    PERFORM set_config('bloem.membership_policy_mirroring', 'on', true);
    UPDATE public.users
    SET access_group_id = NEW.access_group_id,
        permissions = NEW.permissions,
        library_ids = NEW.library_ids,
        max_playback_quality = NEW.max_playback_quality,
        max_streams = NEW.max_streams,
        max_transcodes = NEW.max_transcodes,
        transcode_allowed = NEW.transcode_allowed,
        audio_transcode_allowed = NEW.audio_transcode_allowed,
        download_allowed = NEW.download_allowed,
        download_transcode_allowed = NEW.download_transcode_allowed,
        requests_allowed = NEW.requests_allowed,
        max_profiles = NEW.max_profiles,
        access_policy_revision = NEW.access_policy_revision,
        updated_at = now()
    WHERE id = NEW.account_id
      AND ROW(users.access_group_id, users.permissions, users.library_ids, users.max_playback_quality, users.max_streams, users.max_transcodes, users.transcode_allowed, users.audio_transcode_allowed, users.download_allowed, users.download_transcode_allowed, users.requests_allowed, users.max_profiles, users.access_policy_revision) IS DISTINCT FROM ROW(NEW.access_group_id, NEW.permissions, NEW.library_ids, NEW.max_playback_quality, NEW.max_streams, NEW.max_transcodes, NEW.transcode_allowed, NEW.audio_transcode_allowed, NEW.download_allowed, NEW.download_transcode_allowed, NEW.requests_allowed, NEW.max_profiles, NEW.access_policy_revision);
    PERFORM set_config('bloem.membership_policy_mirroring', COALESCE(previous_mirroring, ''), true);
    RETURN NULL;
END;
$$;

CREATE TRIGGER organization_memberships_policy_mirror_to_user
AFTER INSERT OR UPDATE OF access_group_id, permissions, library_ids, max_playback_quality, max_streams, max_transcodes, transcode_allowed, audio_transcode_allowed, download_allowed, download_transcode_allowed, requests_allowed, max_profiles, access_policy_revision
ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.bloem_mirror_membership_policy_to_user();

-- What Silo cannot honor is refused while mirrored: a second organization, a
-- membership outside the default organization, and a profile with its own
-- login (Silo would treat that session as the whole account).
CREATE FUNCTION public.bloem_mirrored_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) <> 'mirrored' THEN
        RETURN NEW;
    END IF;
    -- Nested IFs: PL/pgSQL resolves every NEW field in an expression, so a
    -- table's columns may only be read inside that table's branch.
    IF TG_TABLE_NAME = 'organizations' THEN
        RAISE EXCEPTION 'bloem_silo_switching_single_organization' USING ERRCODE = 'P0001';
    ELSIF TG_TABLE_NAME = 'organization_memberships' THEN
        IF NEW.organization_id IS DISTINCT FROM public.bloem_default_organization_id() THEN
            RAISE EXCEPTION 'bloem_silo_switching_single_organization' USING ERRCODE = 'P0001';
        END IF;
    ELSIF TG_TABLE_NAME = 'user_profiles' THEN
        IF NEW.login_email IS NOT NULL OR NEW.password_hash IS NOT NULL THEN
            RAISE EXCEPTION 'bloem_silo_switching_direct_profile_login' USING ERRCODE = 'P0001';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER organizations_mirrored_single
BEFORE INSERT ON public.organizations
FOR EACH ROW EXECUTE FUNCTION public.bloem_mirrored_guard();

CREATE TRIGGER organization_memberships_mirrored_default_only
BEFORE INSERT OR UPDATE OF organization_id ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.bloem_mirrored_guard();

CREATE TRIGGER user_profiles_mirrored_no_direct_login
BEFORE INSERT OR UPDATE OF login_email, password_hash ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_mirrored_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.membership_policy_authority WHERE phase = 'mirrored') THEN
        RAISE EXCEPTION 'cannot roll back the mirrored phase while the database is mirrored';
    END IF;
END;
$$;

DROP TRIGGER user_profiles_mirrored_no_direct_login ON public.user_profiles;
DROP TRIGGER organization_memberships_mirrored_default_only ON public.organization_memberships;
DROP TRIGGER organizations_mirrored_single ON public.organizations;
DROP FUNCTION public.bloem_mirrored_guard();
DROP TRIGGER organization_memberships_policy_mirror_to_user ON public.organization_memberships;
DROP FUNCTION public.bloem_mirror_membership_policy_to_user();
DROP TRIGGER users_policy_mirror_to_membership ON public.users;
DROP FUNCTION public.bloem_mirror_user_policy_to_membership();

CREATE OR REPLACE FUNCTION public.fence_legacy_user_policy_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    PERFORM phase FROM public.membership_policy_authority WHERE singleton;
    IF NEW.access_group_id IS DISTINCT FROM OLD.access_group_id
       OR NEW.permissions IS DISTINCT FROM OLD.permissions
       OR NEW.library_ids IS DISTINCT FROM OLD.library_ids
       OR NEW.max_playback_quality IS DISTINCT FROM OLD.max_playback_quality
       OR NEW.max_streams IS DISTINCT FROM OLD.max_streams
       OR NEW.max_transcodes IS DISTINCT FROM OLD.max_transcodes
       OR NEW.transcode_allowed IS DISTINCT FROM OLD.transcode_allowed
       OR NEW.audio_transcode_allowed IS DISTINCT FROM OLD.audio_transcode_allowed
       OR NEW.download_allowed IS DISTINCT FROM OLD.download_allowed
       OR NEW.download_transcode_allowed IS DISTINCT FROM OLD.download_transcode_allowed
       OR NEW.requests_allowed IS DISTINCT FROM OLD.requests_allowed
       OR NEW.max_profiles IS DISTINCT FROM OLD.max_profiles
       OR NEW.access_policy_revision IS DISTINCT FROM OLD.access_policy_revision THEN
        RAISE EXCEPTION 'membership_policy_fenced' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.seed_legacy_membership_policy()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    marked boolean := current_setting('bloem.membership_policy_writer', true) = 'v1';
    account public.users%ROWTYPE;
    account_group_organization uuid;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;
    IF marked AND authority_phase <> 'finalized' THEN
        RAISE EXCEPTION 'membership_policy_not_finalized' USING ERRCODE = 'P0001';
    ELSIF NOT marked AND authority_phase = 'finalized' THEN
        RAISE EXCEPTION 'membership_policy_finalized' USING ERRCODE = 'P0001';
    ELSIF marked THEN
        RETURN NEW;
    END IF;

    SELECT * INTO STRICT account FROM public.users WHERE id = NEW.account_id;
    SELECT organization_id INTO account_group_organization
    FROM public.access_groups WHERE id = account.access_group_id;
    NEW.access_group_id := CASE
        WHEN account_group_organization = NEW.organization_id THEN account.access_group_id
        ELSE (SELECT id FROM public.access_groups WHERE organization_id = NEW.organization_id AND is_default)
    END;
    IF NEW.status = 'active' AND account.role <> 'admin' AND NEW.access_group_id IS NULL THEN
        RAISE EXCEPTION 'membership_policy_missing_organization_group' USING ERRCODE = 'P0001';
    END IF;
    NEW.permissions := account.permissions;
    NEW.library_ids := account.library_ids;
    NEW.max_playback_quality := account.max_playback_quality;
    NEW.max_streams := account.max_streams;
    NEW.max_transcodes := account.max_transcodes;
    NEW.transcode_allowed := account.transcode_allowed;
    NEW.audio_transcode_allowed := account.audio_transcode_allowed;
    NEW.download_allowed := account.download_allowed;
    NEW.download_transcode_allowed := account.download_transcode_allowed;
    NEW.requests_allowed := account.requests_allowed;
    NEW.max_profiles := account.max_profiles;
    NEW.access_policy_revision := account.access_policy_revision;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_membership_policy_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;
    IF current_setting('bloem.membership_policy_writer', true) <> 'v1'
       OR authority_phase <> 'finalized' THEN
        RAISE EXCEPTION 'membership_policy_not_finalized' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_membership_policy_authority_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'membership_policy_authority_immutable' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.singleton IS DISTINCT FROM OLD.singleton
       OR NEW.fenced_at IS DISTINCT FROM OLD.fenced_at
       OR OLD.phase <> 'compatibility'
       OR NEW.phase <> 'finalized'
       OR NEW.finalized_at IS NULL
	   OR NEW.finalized_at < OLD.fenced_at
       OR current_setting('bloem.membership_policy_finalizer', true) <> 'v1' THEN
        RAISE EXCEPTION 'membership_policy_authority_immutable' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_membership_policy_rollout_observation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    current_observation bigint;
	current_heartbeat_at timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'membership_policy_observation_immutable' USING ERRCODE = 'P0001';
    END IF;
    IF current_setting('bloem.membership_policy_observation_writer', true) = 'heartbeat_v1' THEN
        IF TG_OP = 'INSERT' THEN
            RETURN NEW;
        END IF;
        IF NEW.observation_id IS DISTINCT FROM OLD.observation_id
           OR NEW.node_id IS DISTINCT FROM OLD.node_id
           OR NEW.node_type IS DISTINCT FROM OLD.node_type
           OR NEW.state IS DISTINCT FROM OLD.state
           OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
           OR NEW.observed_at IS DISTINCT FROM OLD.observed_at
           OR NEW.drained_at IS DISTINCT FROM OLD.drained_at
           OR NEW.last_seen_at < OLD.last_seen_at THEN
            RAISE EXCEPTION 'membership_policy_observation_immutable' USING ERRCODE = 'P0001';
        END IF;
        RETURN NEW;
    END IF;
    IF current_setting('bloem.membership_policy_observation_writer', true) = 'operator_drain_v1'
       AND TG_OP = 'UPDATE'
       AND OLD.state = 'legacy'
       AND NEW.state = 'drained'
       AND NEW.observation_id = OLD.observation_id
       AND NEW.node_id = OLD.node_id
       AND NEW.node_type = OLD.node_type
       AND NEW.instance_id IS NULL
       AND NEW.observed_at = OLD.observed_at
       AND NEW.last_seen_at = OLD.last_seen_at
       AND NEW.drained_at IS NOT NULL
       AND NEW.drained_at >= OLD.last_seen_at THEN
        SELECT phase INTO authority_phase FROM public.membership_policy_authority WHERE singleton;
        SELECT membership_policy_rollout_observation_id, updated_at
		INTO current_observation, current_heartbeat_at
        FROM public.node_heartbeats WHERE node_id = OLD.node_id;
        IF authority_phase = 'compatibility'
		   AND (
			current_observation IS DISTINCT FROM OLD.observation_id
			OR current_heartbeat_at < clock_timestamp() - interval '5 minutes'
		   ) THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION 'membership_policy_observation_immutable' USING ERRCODE = 'P0001';
END;
$$;

CREATE OR REPLACE FUNCTION public.guard_node_heartbeat_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    observation_state text;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;
    IF current_setting('bloem.heartbeat_cleanup_writer', true) = 'v1'
       AND current_setting('bloem.heartbeat_cleanup_node_id', true) = OLD.node_id
       AND current_setting('bloem.heartbeat_cleanup_instance_id', true) = OLD.instance_id::text THEN
        RETURN OLD;
    END IF;
    IF authority_phase = 'compatibility'
       AND OLD.instance_id IS NULL
       AND OLD.membership_policy_rollout_observation_id IS NOT NULL THEN
        SELECT state INTO observation_state
        FROM public.membership_policy_rollout_observations
        WHERE observation_id = OLD.membership_policy_rollout_observation_id;
        IF observation_state = 'legacy' THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'membership_policy_heartbeat_delete_fenced' USING ERRCODE = 'P0001';
END;
$$;

DROP FUNCTION public.bloem_membership_policy_writer_marker();

ALTER TABLE public.membership_policy_authority
    DROP CONSTRAINT membership_policy_authority_check,
    DROP CONSTRAINT membership_policy_authority_phase_check,
    DROP COLUMN mirrored_at,
    ADD CONSTRAINT membership_policy_authority_phase_check
        CHECK (phase IN ('compatibility', 'finalized')),
    ADD CONSTRAINT membership_policy_authority_check CHECK (
        (phase = 'compatibility' AND finalized_at IS NULL)
        OR (phase = 'finalized' AND finalized_at IS NOT NULL)
    );
-- +goose StatementEnd
