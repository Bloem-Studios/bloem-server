-- +goose Up
-- +goose StatementBegin
LOCK TABLE public.users, public.organization_memberships, public.user_profiles, public.node_heartbeats IN ACCESS EXCLUSIVE MODE;

CREATE TABLE public.legacy_user_policy_rollback_snapshot (
    account_id integer PRIMARY KEY,
    access_group_id bigint,
    permissions text[] NOT NULL,
    library_ids integer[],
    max_playback_quality text,
    max_streams integer,
    max_transcodes integer,
    transcode_allowed boolean,
    audio_transcode_allowed boolean,
    download_allowed boolean,
    download_transcode_allowed boolean,
    requests_allowed boolean,
    max_profiles integer NOT NULL,
    access_policy_revision bigint NOT NULL
);

INSERT INTO public.legacy_user_policy_rollback_snapshot (
    account_id, access_group_id, permissions, library_ids,
    max_playback_quality, max_streams, max_transcodes,
    transcode_allowed, audio_transcode_allowed, download_allowed,
    download_transcode_allowed, requests_allowed, max_profiles,
    access_policy_revision
)
SELECT id, access_group_id, permissions, library_ids,
       max_playback_quality, max_streams, max_transcodes,
       transcode_allowed, audio_transcode_allowed, download_allowed,
       download_transcode_allowed, requests_allowed, max_profiles,
       access_policy_revision
FROM public.users;

CREATE TABLE public.profile_primary_rollback_snapshot (
	account_id integer NOT NULL,
	profile_id text NOT NULL,
    pre_is_primary boolean NOT NULL,
	post_is_primary boolean,
	PRIMARY KEY (account_id, profile_id)
);

INSERT INTO public.profile_primary_rollback_snapshot (account_id, profile_id, pre_is_primary)
SELECT user_id, id, is_primary
FROM public.user_profiles;

ALTER TABLE public.organization_memberships
    ADD COLUMN access_group_id bigint,
    ADD COLUMN permissions text[] NOT NULL DEFAULT '{}'::text[],
    ADD COLUMN library_ids integer[],
    ADD COLUMN max_playback_quality text,
    ADD COLUMN max_streams integer,
    ADD COLUMN max_transcodes integer,
    ADD COLUMN transcode_allowed boolean,
    ADD COLUMN audio_transcode_allowed boolean,
    ADD COLUMN download_allowed boolean,
    ADD COLUMN download_transcode_allowed boolean,
    ADD COLUMN requests_allowed boolean,
    ADD COLUMN max_profiles integer NOT NULL DEFAULT 5,
    ADD COLUMN access_policy_revision bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT organization_memberships_max_profiles_min_check CHECK (max_profiles >= 1),
    ADD CONSTRAINT organization_memberships_access_policy_revision_check CHECK (access_policy_revision > 0);

UPDATE public.organization_memberships AS memberships
SET access_group_id = CASE
        WHEN EXISTS (
            SELECT 1
            FROM public.access_groups AS account_group
            WHERE account_group.id = account.access_group_id
              AND account_group.organization_id = memberships.organization_id
        )
            THEN account.access_group_id
        ELSE (
            SELECT organization_default.id
            FROM public.access_groups AS organization_default
            WHERE organization_default.organization_id = memberships.organization_id
              AND organization_default.is_default
        )
    END,
    permissions = account.permissions,
    library_ids = account.library_ids,
    max_playback_quality = account.max_playback_quality,
    max_streams = account.max_streams,
    max_transcodes = account.max_transcodes,
    transcode_allowed = account.transcode_allowed,
    audio_transcode_allowed = account.audio_transcode_allowed,
    download_allowed = account.download_allowed,
    download_transcode_allowed = account.download_transcode_allowed,
    requests_allowed = account.requests_allowed,
    max_profiles = account.max_profiles,
    access_policy_revision = account.access_policy_revision
FROM public.users AS account
WHERE account.id = memberships.account_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.organization_memberships AS memberships
        JOIN public.users AS account ON account.id = memberships.account_id
        WHERE memberships.status = 'active'
          AND account.role <> 'admin'
          AND memberships.access_group_id IS NULL
    ) THEN
        RAISE EXCEPTION 'membership policy migration found active non-platform membership without organization access group';
    END IF;
END;
$$;

ALTER TABLE public.organization_memberships
    ADD CONSTRAINT organization_memberships_organization_access_group_fkey
    FOREIGN KEY (organization_id, access_group_id)
    REFERENCES public.access_groups(organization_id, id)
    ON DELETE RESTRICT;

ALTER TABLE public.user_profiles
    ADD CONSTRAINT user_profiles_organization_membership_fkey
    FOREIGN KEY (organization_id, user_id)
    REFERENCES public.organization_memberships(organization_id, account_id)
    ON DELETE CASCADE;

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY organization_id, user_id
               ORDER BY created_at, id
           ) AS rank
    FROM public.user_profiles
    WHERE is_primary
)
UPDATE public.user_profiles AS profiles
SET is_primary = false
FROM ranked
WHERE profiles.id = ranked.id
  AND ranked.rank > 1;

DROP INDEX public.user_profiles_primary_per_user;
CREATE UNIQUE INDEX user_profiles_primary_per_organization_user
    ON public.user_profiles(organization_id, user_id)
    WHERE is_primary;

UPDATE public.profile_primary_rollback_snapshot AS snapshots
SET post_is_primary = profiles.is_primary
FROM public.user_profiles AS profiles
WHERE profiles.user_id = snapshots.account_id
  AND profiles.id = snapshots.profile_id;

CREATE TABLE public.membership_policy_rollback_snapshot (
    membership_id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    account_id integer NOT NULL,
    access_group_id bigint,
    permissions text[] NOT NULL,
    library_ids integer[],
    max_playback_quality text,
    max_streams integer,
    max_transcodes integer,
    transcode_allowed boolean,
    audio_transcode_allowed boolean,
    download_allowed boolean,
    download_transcode_allowed boolean,
    requests_allowed boolean,
    max_profiles integer NOT NULL,
    access_policy_revision bigint NOT NULL
);

INSERT INTO public.membership_policy_rollback_snapshot (
    membership_id, organization_id, account_id, access_group_id,
    permissions, library_ids, max_playback_quality, max_streams,
    max_transcodes, transcode_allowed, audio_transcode_allowed,
    download_allowed, download_transcode_allowed, requests_allowed,
    max_profiles, access_policy_revision
)
SELECT id, organization_id, account_id, access_group_id,
       permissions, library_ids, max_playback_quality, max_streams,
       max_transcodes, transcode_allowed, audio_transcode_allowed,
       download_allowed, download_transcode_allowed, requests_allowed,
       max_profiles, access_policy_revision
FROM public.organization_memberships;

DROP TRIGGER user_profiles_entitlement_limit ON public.user_profiles;
DROP FUNCTION public.enforce_user_profile_entitlement_limit();

CREATE FUNCTION public.enforce_user_profile_entitlement_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    membership_limit integer;
    group_limit integer;
    group_managed boolean;
    effective_limit integer;
    existing_profiles integer;
BEGIN
	IF NEW.organization_id IS NULL OR NEW.user_id IS NULL THEN
		RETURN NEW;
	END IF;
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.organization_id::text || ':' || NEW.user_id::text, 0)
    );
    SELECT memberships.max_profiles,
           groups.max_profiles,
           groups.managed_template_key IS NOT NULL
      INTO membership_limit, group_limit, group_managed
      FROM public.organization_memberships AS memberships
      LEFT JOIN public.access_groups AS groups
        ON groups.id = NEW.access_group_id
       AND groups.organization_id = NEW.organization_id
     WHERE memberships.organization_id = NEW.organization_id
       AND memberships.account_id = NEW.user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'profile owner has no exact organization membership'
            USING ERRCODE = '23503', CONSTRAINT = 'user_profiles_organization_membership_fkey';
    END IF;
    IF group_managed AND group_limit = 0 THEN
        group_limit := 1;
    END IF;
    effective_limit := CASE
        WHEN membership_limit > 0 AND group_limit > 0 THEN LEAST(membership_limit, group_limit)
        WHEN membership_limit > 0 THEN membership_limit
        WHEN group_limit > 0 THEN group_limit
        ELSE 0
    END;
    IF effective_limit > 0 THEN
        SELECT count(*)::integer INTO existing_profiles
          FROM public.user_profiles
         WHERE user_id = NEW.user_id
           AND organization_id = NEW.organization_id;
        IF existing_profiles >= effective_limit THEN
            RAISE EXCEPTION 'profile entitlement limit reached'
                USING ERRCODE = '23514', CONSTRAINT = 'user_profiles_entitlement_limit';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER user_profiles_entitlement_limit
BEFORE INSERT ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.enforce_user_profile_entitlement_limit();

CREATE TABLE public.membership_policy_authority (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    phase text NOT NULL CHECK (phase IN ('compatibility', 'finalized')),
    fenced_at timestamptz NOT NULL,
    finalized_at timestamptz,
    CHECK (
        (phase = 'compatibility' AND finalized_at IS NULL)
        OR (phase = 'finalized' AND finalized_at IS NOT NULL)
    )
);

CREATE TABLE public.membership_policy_rollout_observations (
    observation_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id text NOT NULL,
    node_type text NOT NULL,
    state text NOT NULL CHECK (state IN ('legacy', 'capable', 'drained')),
    instance_id uuid,
    observed_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    drained_at timestamptz,
    UNIQUE (node_id, observed_at),
    CHECK (last_seen_at >= observed_at),
    CHECK (
        (state = 'legacy' AND instance_id IS NULL AND drained_at IS NULL)
        OR (state = 'capable' AND instance_id IS NOT NULL AND drained_at IS NULL)
        OR (state = 'drained' AND instance_id IS NULL AND drained_at IS NOT NULL AND drained_at >= last_seen_at)
    )
);

CREATE UNIQUE INDEX membership_policy_rollout_capable_instance_idx
    ON public.membership_policy_rollout_observations(node_id, instance_id)
    WHERE state = 'capable';

ALTER TABLE public.node_heartbeats
    ADD COLUMN schema_capabilities text[] NOT NULL DEFAULT '{}'::text[],
    ADD COLUMN instance_id uuid,
    ADD COLUMN membership_policy_rollout_observation_id bigint;

INSERT INTO public.membership_policy_rollout_observations (
    node_id, node_type, state, instance_id, observed_at, last_seen_at
)
SELECT node_id, node_type, 'legacy', NULL, updated_at, updated_at
FROM public.node_heartbeats
WHERE node_type IN ('integrated', 'api');

UPDATE public.node_heartbeats AS heartbeats
SET membership_policy_rollout_observation_id = observations.observation_id
FROM public.membership_policy_rollout_observations AS observations
WHERE observations.node_id = heartbeats.node_id
  AND observations.observed_at = heartbeats.updated_at
  AND observations.state = 'legacy';

ALTER TABLE public.node_heartbeats
    ADD CONSTRAINT node_heartbeats_membership_policy_observation_fkey
    FOREIGN KEY (membership_policy_rollout_observation_id)
    REFERENCES public.membership_policy_rollout_observations(observation_id)
    ON DELETE RESTRICT;

CREATE FUNCTION public.fence_legacy_user_policy_write()
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

CREATE TRIGGER users_membership_policy_authority_fence
BEFORE UPDATE ON public.users
FOR EACH ROW EXECUTE FUNCTION public.fence_legacy_user_policy_write();

CREATE FUNCTION public.seed_legacy_membership_policy()
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

CREATE TRIGGER organization_memberships_legacy_policy_seed
BEFORE INSERT ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.seed_legacy_membership_policy();

CREATE FUNCTION public.guard_membership_policy_write()
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

CREATE TRIGGER organization_memberships_policy_writer_guard
BEFORE UPDATE OF access_group_id, permissions, library_ids, max_playback_quality,
    max_streams, max_transcodes, transcode_allowed, audio_transcode_allowed,
    download_allowed, download_transcode_allowed, requests_allowed,
    max_profiles, access_policy_revision
ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.guard_membership_policy_write();

CREATE FUNCTION public.guard_membership_policy_authority_transition()
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

CREATE TRIGGER membership_policy_authority_transition_guard
BEFORE UPDATE OR DELETE ON public.membership_policy_authority
FOR EACH ROW EXECUTE FUNCTION public.guard_membership_policy_authority_transition();

CREATE FUNCTION public.guard_membership_policy_rollout_observation()
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

CREATE TRIGGER membership_policy_rollout_observation_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.membership_policy_rollout_observations
FOR EACH ROW EXECUTE FUNCTION public.guard_membership_policy_rollout_observation();

CREATE FUNCTION public.register_membership_policy_heartbeat()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    authority_phase text;
    observation_state text;
    observation bigint;
    previous_marker text := current_setting('bloem.membership_policy_observation_writer', true);
    marked boolean := current_setting('bloem.schema_capability_writer', true) = 'v1';
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('bloem.membership_policy_handoff', 0));
    SELECT phase INTO STRICT authority_phase
    FROM public.membership_policy_authority
    WHERE singleton;

    IF current_setting('bloem.node_heartbeat_schema_extension', true) = 'tenant_session_revocation_v1' THEN
        IF TG_OP = 'UPDATE'
           AND NEW.node_id IS NOT DISTINCT FROM OLD.node_id
           AND NEW.node_type IS NOT DISTINCT FROM OLD.node_type
           AND NEW.node_url IS NOT DISTINCT FROM OLD.node_url
           AND NEW.updated_at IS NOT DISTINCT FROM OLD.updated_at
           AND NEW.schema_capabilities IS NOT DISTINCT FROM OLD.schema_capabilities
           AND NEW.instance_id IS NOT DISTINCT FROM OLD.instance_id
           AND NEW.membership_policy_rollout_observation_id IS NOT DISTINCT FROM OLD.membership_policy_rollout_observation_id THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION 'membership_policy_schema_extension_invalid' USING ERRCODE = 'P0001';
    END IF;

    IF NEW.node_type NOT IN ('integrated', 'api') THEN
        RETURN NEW;
    END IF;
	-- Legacy and capable writers both use INSERT .. ON CONFLICT DO UPDATE. A
	-- row-level BEFORE INSERT trigger runs even when PostgreSQL will take the
	-- conflict arm, so serialize by node ID and defer all observation work to
	-- the subsequent UPDATE trigger when a current row already exists.
	PERFORM pg_advisory_xact_lock(hashtextextended('bloem.membership_policy_node:' || NEW.node_id, 0));
	IF TG_OP = 'INSERT' AND EXISTS (
		SELECT 1 FROM public.node_heartbeats WHERE node_id = NEW.node_id
	) THEN
		RETURN NEW;
	END IF;
    IF authority_phase = 'finalized' AND NOT marked THEN
        RAISE EXCEPTION 'membership_policy_finalized' USING ERRCODE = 'P0001';
    END IF;

    PERFORM set_config('bloem.membership_policy_observation_writer', 'heartbeat_v1', true);
    IF marked THEN
        IF NEW.instance_id IS NULL OR NOT ('membership_policy_v1' = ANY(NEW.schema_capabilities)) THEN
            RAISE EXCEPTION 'membership_policy_capability_invalid' USING ERRCODE = 'P0001';
        END IF;
        INSERT INTO public.membership_policy_rollout_observations (
            node_id, node_type, state, instance_id, observed_at, last_seen_at
        ) VALUES (
            NEW.node_id, NEW.node_type, 'capable', NEW.instance_id, NEW.updated_at, NEW.updated_at
        )
        ON CONFLICT (node_id, instance_id) WHERE state = 'capable'
        DO UPDATE SET last_seen_at = GREATEST(
            public.membership_policy_rollout_observations.last_seen_at,
            EXCLUDED.last_seen_at
        )
        RETURNING observation_id INTO observation;
        NEW.membership_policy_rollout_observation_id := observation;
    ELSE
        NEW.schema_capabilities := '{}'::text[];
        NEW.instance_id := NULL;
        observation := NULL;
        IF TG_OP = 'UPDATE' AND OLD.membership_policy_rollout_observation_id IS NOT NULL THEN
            SELECT state INTO observation_state
            FROM public.membership_policy_rollout_observations
            WHERE observation_id = OLD.membership_policy_rollout_observation_id
            FOR UPDATE;
            IF observation_state = 'legacy' THEN
                UPDATE public.membership_policy_rollout_observations
                SET last_seen_at = GREATEST(last_seen_at, NEW.updated_at)
                WHERE observation_id = OLD.membership_policy_rollout_observation_id
                RETURNING observation_id INTO observation;
            END IF;
        END IF;
        IF observation IS NULL THEN
			-- A legacy INSERT .. ON CONFLICT DO UPDATE fires both row-level
			-- BEFORE triggers. The INSERT leg has already created this exact
			-- observation, so the UPDATE leg must reuse it rather than inventing
			-- a duplicate or rewriting an older linked observation.
			SELECT observation_id INTO observation
			FROM public.membership_policy_rollout_observations
			WHERE node_id = NEW.node_id
			  AND observed_at = NEW.updated_at
			  AND state = 'legacy';
			IF observation IS NULL THEN
				INSERT INTO public.membership_policy_rollout_observations (
					node_id, node_type, state, instance_id, observed_at, last_seen_at
				) VALUES (
					NEW.node_id, NEW.node_type, 'legacy', NULL, NEW.updated_at, NEW.updated_at
				) RETURNING observation_id INTO observation;
			END IF;
        END IF;
        NEW.membership_policy_rollout_observation_id := observation;
    END IF;
    PERFORM set_config('bloem.membership_policy_observation_writer', COALESCE(previous_marker, ''), true);
    RETURN NEW;
END;
$$;

CREATE TRIGGER node_heartbeats_10_membership_policy_registration
BEFORE INSERT OR UPDATE ON public.node_heartbeats
FOR EACH ROW EXECUTE FUNCTION public.register_membership_policy_heartbeat();

CREATE FUNCTION public.guard_node_heartbeat_delete()
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

CREATE TRIGGER node_heartbeats_membership_policy_delete_guard
BEFORE DELETE ON public.node_heartbeats
FOR EACH ROW EXECUTE FUNCTION public.guard_node_heartbeat_delete();

INSERT INTO public.membership_policy_authority (
    singleton, phase, fenced_at, finalized_at
) VALUES (true, 'compatibility', now(), NULL);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE public.users, public.organization_memberships, public.user_profiles, public.node_heartbeats IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
	-- An account whose surviving original memberships still equal their Up
	-- snapshots and which has no new membership restores its exact legacy row.
	-- Otherwise every current membership must collapse to one identical tuple;
	-- choosing an arbitrary tenant is forbidden.
	IF EXISTS (
		WITH account_projection AS (
			SELECT account_id,
			       NOT EXISTS (
					SELECT 1
					FROM public.organization_memberships AS current_membership
					LEFT JOIN public.membership_policy_rollback_snapshot AS snapshot
					  ON snapshot.membership_id = current_membership.id
					WHERE current_membership.account_id = accounts.account_id
					  AND (
						snapshot.membership_id IS NULL
						OR current_membership.organization_id IS DISTINCT FROM snapshot.organization_id
						OR current_membership.account_id IS DISTINCT FROM snapshot.account_id
						OR current_membership.access_group_id IS DISTINCT FROM snapshot.access_group_id
						OR current_membership.permissions IS DISTINCT FROM snapshot.permissions
						OR current_membership.library_ids IS DISTINCT FROM snapshot.library_ids
						OR current_membership.max_playback_quality IS DISTINCT FROM snapshot.max_playback_quality
						OR current_membership.max_streams IS DISTINCT FROM snapshot.max_streams
						OR current_membership.max_transcodes IS DISTINCT FROM snapshot.max_transcodes
						OR current_membership.transcode_allowed IS DISTINCT FROM snapshot.transcode_allowed
						OR current_membership.audio_transcode_allowed IS DISTINCT FROM snapshot.audio_transcode_allowed
						OR current_membership.download_allowed IS DISTINCT FROM snapshot.download_allowed
						OR current_membership.download_transcode_allowed IS DISTINCT FROM snapshot.download_transcode_allowed
						OR current_membership.requests_allowed IS DISTINCT FROM snapshot.requests_allowed
						OR current_membership.max_profiles IS DISTINCT FROM snapshot.max_profiles
						OR current_membership.access_policy_revision IS DISTINCT FROM snapshot.access_policy_revision
					  )
			       ) AS restores_legacy
			FROM (
				SELECT account_id FROM public.legacy_user_policy_rollback_snapshot
				UNION
				SELECT account_id FROM public.organization_memberships
			) AS accounts
		)
		SELECT 1
		FROM account_projection AS projection
		JOIN public.organization_memberships AS left_membership
		  ON left_membership.account_id = projection.account_id
		JOIN public.organization_memberships AS right_membership
		  ON right_membership.account_id = projection.account_id
		 AND right_membership.id > left_membership.id
		WHERE NOT projection.restores_legacy
		  AND ROW(
			left_membership.access_group_id, left_membership.permissions,
			left_membership.library_ids, left_membership.max_playback_quality,
			left_membership.max_streams, left_membership.max_transcodes,
			left_membership.transcode_allowed, left_membership.audio_transcode_allowed,
			left_membership.download_allowed, left_membership.download_transcode_allowed,
			left_membership.requests_allowed, left_membership.max_profiles,
			left_membership.access_policy_revision
		  ) IS DISTINCT FROM ROW(
			right_membership.access_group_id, right_membership.permissions,
			right_membership.library_ids, right_membership.max_playback_quality,
			right_membership.max_streams, right_membership.max_transcodes,
			right_membership.transcode_allowed, right_membership.audio_transcode_allowed,
			right_membership.download_allowed, right_membership.download_transcode_allowed,
			right_membership.requests_allowed, right_membership.max_profiles,
			right_membership.access_policy_revision
		  )
	) THEN
		RAISE EXCEPTION 'membership policy Down cannot represent changed membership state';
	END IF;
    IF EXISTS (
        SELECT 1
        FROM public.profile_primary_rollback_snapshot AS snapshots
		JOIN public.user_profiles AS profiles
		  ON profiles.user_id = snapshots.account_id
		 AND profiles.id = snapshots.profile_id
        WHERE profiles.is_primary IS DISTINCT FROM snapshots.post_is_primary
    ) THEN
        RAISE EXCEPTION 'membership policy Down cannot represent changed profile primary state';
    END IF;
	IF EXISTS (
		SELECT profiles.user_id
		FROM public.user_profiles AS profiles
		LEFT JOIN public.profile_primary_rollback_snapshot AS snapshots
		  ON snapshots.account_id = profiles.user_id
		 AND snapshots.profile_id = profiles.id
		GROUP BY profiles.user_id
		HAVING count(*) FILTER (
			WHERE COALESCE(snapshots.pre_is_primary, profiles.is_primary)
		) > 1
	) THEN
		RAISE EXCEPTION 'membership policy Down would restore multiple global primary profiles';
	END IF;
	IF NOT EXISTS (
		SELECT 1 FROM public.membership_policy_authority
		WHERE phase IN ('compatibility', 'finalized')
	) THEN
		RAISE EXCEPTION 'membership policy Down found invalid authority phase';
    END IF;
END;
$$;

DO $$
DECLARE
	authority_phase text;
	policy_column text;
BEGIN
	SELECT phase INTO STRICT authority_phase
	FROM public.membership_policy_authority
	WHERE singleton;
	FOREACH policy_column IN ARRAY ARRAY[
		'access_group_id', 'permissions', 'library_ids',
		'max_playback_quality', 'max_streams', 'max_transcodes',
		'transcode_allowed', 'audio_transcode_allowed', 'download_allowed',
		'download_transcode_allowed', 'requests_allowed', 'max_profiles',
		'access_policy_revision'
	] LOOP
		IF authority_phase = 'compatibility' THEN
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users' AND column_name=policy_column
			) OR EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users'
				  AND column_name='rollback_membership_' || policy_column
			) THEN
				RAISE EXCEPTION 'membership policy Down found half-renamed compatibility projection';
			END IF;
		ELSE
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users' AND column_name=policy_column
			) OR NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name='users'
				  AND column_name='rollback_membership_' || policy_column
			) THEN
				RAISE EXCEPTION 'membership policy Down found half-renamed finalized projection';
			END IF;
		END IF;
	END LOOP;
END;
$$;

DO $$
DECLARE
	policy_column text;
BEGIN
	IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized' THEN
		FOREACH policy_column IN ARRAY ARRAY[
			'access_group_id', 'permissions', 'library_ids',
			'max_playback_quality', 'max_streams', 'max_transcodes',
			'transcode_allowed', 'audio_transcode_allowed', 'download_allowed',
			'download_transcode_allowed', 'requests_allowed', 'max_profiles',
			'access_policy_revision'
		] LOOP
			EXECUTE format(
				'ALTER TABLE public.users RENAME COLUMN %I TO %I',
				'rollback_membership_' || policy_column,
				policy_column
			);
		END LOOP;
	END IF;
END;
$$;

DROP TRIGGER IF EXISTS users_membership_policy_authority_fence ON public.users;

UPDATE public.users AS accounts
SET access_group_id = snapshots.access_group_id,
    permissions = snapshots.permissions,
    library_ids = snapshots.library_ids,
    max_playback_quality = snapshots.max_playback_quality,
    max_streams = snapshots.max_streams,
    max_transcodes = snapshots.max_transcodes,
    transcode_allowed = snapshots.transcode_allowed,
    audio_transcode_allowed = snapshots.audio_transcode_allowed,
    download_allowed = snapshots.download_allowed,
    download_transcode_allowed = snapshots.download_transcode_allowed,
    requests_allowed = snapshots.requests_allowed,
    max_profiles = snapshots.max_profiles,
    access_policy_revision = snapshots.access_policy_revision
FROM public.legacy_user_policy_rollback_snapshot AS snapshots
WHERE accounts.id = snapshots.account_id
  AND NOT EXISTS (
	SELECT 1
	FROM public.organization_memberships AS current_membership
	LEFT JOIN public.membership_policy_rollback_snapshot AS snapshot
	  ON snapshot.membership_id = current_membership.id
	WHERE current_membership.account_id = accounts.id
	  AND (
		snapshot.membership_id IS NULL
		OR current_membership.organization_id IS DISTINCT FROM snapshot.organization_id
		OR current_membership.account_id IS DISTINCT FROM snapshot.account_id
		OR current_membership.access_group_id IS DISTINCT FROM snapshot.access_group_id
		OR current_membership.permissions IS DISTINCT FROM snapshot.permissions
		OR current_membership.library_ids IS DISTINCT FROM snapshot.library_ids
		OR current_membership.max_playback_quality IS DISTINCT FROM snapshot.max_playback_quality
		OR current_membership.max_streams IS DISTINCT FROM snapshot.max_streams
		OR current_membership.max_transcodes IS DISTINCT FROM snapshot.max_transcodes
		OR current_membership.transcode_allowed IS DISTINCT FROM snapshot.transcode_allowed
		OR current_membership.audio_transcode_allowed IS DISTINCT FROM snapshot.audio_transcode_allowed
		OR current_membership.download_allowed IS DISTINCT FROM snapshot.download_allowed
		OR current_membership.download_transcode_allowed IS DISTINCT FROM snapshot.download_transcode_allowed
		OR current_membership.requests_allowed IS DISTINCT FROM snapshot.requests_allowed
		OR current_membership.max_profiles IS DISTINCT FROM snapshot.max_profiles
		OR current_membership.access_policy_revision IS DISTINCT FROM snapshot.access_policy_revision
	  )
  );

WITH collapse AS (
	SELECT DISTINCT ON (memberships.account_id)
	       memberships.*
	FROM public.organization_memberships AS memberships
	WHERE EXISTS (
		SELECT 1
		FROM public.organization_memberships AS current_membership
		LEFT JOIN public.membership_policy_rollback_snapshot AS snapshot
		  ON snapshot.membership_id = current_membership.id
		WHERE current_membership.account_id = memberships.account_id
		  AND (
			snapshot.membership_id IS NULL
			OR current_membership.organization_id IS DISTINCT FROM snapshot.organization_id
			OR current_membership.account_id IS DISTINCT FROM snapshot.account_id
			OR current_membership.access_group_id IS DISTINCT FROM snapshot.access_group_id
			OR current_membership.permissions IS DISTINCT FROM snapshot.permissions
			OR current_membership.library_ids IS DISTINCT FROM snapshot.library_ids
			OR current_membership.max_playback_quality IS DISTINCT FROM snapshot.max_playback_quality
			OR current_membership.max_streams IS DISTINCT FROM snapshot.max_streams
			OR current_membership.max_transcodes IS DISTINCT FROM snapshot.max_transcodes
			OR current_membership.transcode_allowed IS DISTINCT FROM snapshot.transcode_allowed
			OR current_membership.audio_transcode_allowed IS DISTINCT FROM snapshot.audio_transcode_allowed
			OR current_membership.download_allowed IS DISTINCT FROM snapshot.download_allowed
			OR current_membership.download_transcode_allowed IS DISTINCT FROM snapshot.download_transcode_allowed
			OR current_membership.requests_allowed IS DISTINCT FROM snapshot.requests_allowed
			OR current_membership.max_profiles IS DISTINCT FROM snapshot.max_profiles
			OR current_membership.access_policy_revision IS DISTINCT FROM snapshot.access_policy_revision
		  )
	)
	ORDER BY memberships.account_id, memberships.organization_id, memberships.id
)
UPDATE public.users AS accounts
SET access_group_id = collapse.access_group_id,
	permissions = collapse.permissions,
	library_ids = collapse.library_ids,
	max_playback_quality = collapse.max_playback_quality,
	max_streams = collapse.max_streams,
	max_transcodes = collapse.max_transcodes,
	transcode_allowed = collapse.transcode_allowed,
	audio_transcode_allowed = collapse.audio_transcode_allowed,
	download_allowed = collapse.download_allowed,
	download_transcode_allowed = collapse.download_transcode_allowed,
	requests_allowed = collapse.requests_allowed,
	max_profiles = collapse.max_profiles,
	access_policy_revision = collapse.access_policy_revision
FROM collapse
WHERE accounts.id = collapse.account_id;

UPDATE public.user_profiles AS profiles
SET is_primary = snapshots.pre_is_primary
FROM public.profile_primary_rollback_snapshot AS snapshots
WHERE profiles.user_id = snapshots.account_id
  AND profiles.id = snapshots.profile_id;

DROP TRIGGER node_heartbeats_membership_policy_delete_guard ON public.node_heartbeats;
DROP FUNCTION public.guard_node_heartbeat_delete();
DROP TRIGGER node_heartbeats_10_membership_policy_registration ON public.node_heartbeats;
DROP FUNCTION public.register_membership_policy_heartbeat();
DROP TRIGGER membership_policy_rollout_observation_guard ON public.membership_policy_rollout_observations;
DROP FUNCTION public.guard_membership_policy_rollout_observation();
DROP TRIGGER membership_policy_authority_transition_guard ON public.membership_policy_authority;
DROP FUNCTION public.guard_membership_policy_authority_transition();
DROP TRIGGER organization_memberships_policy_writer_guard ON public.organization_memberships;
DROP FUNCTION public.guard_membership_policy_write();
DROP TRIGGER organization_memberships_legacy_policy_seed ON public.organization_memberships;
DROP FUNCTION public.seed_legacy_membership_policy();
DROP FUNCTION public.fence_legacy_user_policy_write();

ALTER TABLE public.node_heartbeats
    DROP CONSTRAINT node_heartbeats_membership_policy_observation_fkey,
    DROP COLUMN membership_policy_rollout_observation_id,
    DROP COLUMN instance_id,
    DROP COLUMN schema_capabilities;

DROP INDEX public.membership_policy_rollout_capable_instance_idx;
DROP TABLE public.membership_policy_rollout_observations;
DROP TABLE public.membership_policy_authority;

DROP TRIGGER user_profiles_entitlement_limit ON public.user_profiles;
DROP FUNCTION public.enforce_user_profile_entitlement_limit();

CREATE FUNCTION public.enforce_user_profile_entitlement_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    account_limit integer;
    group_limit integer;
    group_managed boolean;
    effective_limit integer;
    existing_profiles integer;
BEGIN
    PERFORM pg_advisory_xact_lock(NEW.user_id);
    SELECT u.max_profiles, g.max_profiles, g.managed_template_key IS NOT NULL
      INTO account_limit, group_limit, group_managed
      FROM public.users u
      LEFT JOIN public.access_groups g
        ON g.id = NEW.access_group_id
       AND g.organization_id = NEW.organization_id
     WHERE u.id = NEW.user_id;
    IF group_managed AND group_limit = 0 THEN
        group_limit := 1;
    END IF;
    effective_limit := CASE
        WHEN account_limit > 0 AND group_limit > 0 THEN LEAST(account_limit, group_limit)
        WHEN account_limit > 0 THEN account_limit
        WHEN group_limit > 0 THEN group_limit
        ELSE 0
    END;
    IF effective_limit > 0 THEN
        SELECT count(*)::integer INTO existing_profiles
          FROM public.user_profiles
         WHERE user_id = NEW.user_id
           AND organization_id = NEW.organization_id;
        IF existing_profiles >= effective_limit THEN
            RAISE EXCEPTION 'profile entitlement limit reached'
                USING ERRCODE = '23514', CONSTRAINT = 'user_profiles_entitlement_limit';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER user_profiles_entitlement_limit
BEFORE INSERT ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.enforce_user_profile_entitlement_limit();

DROP INDEX public.user_profiles_primary_per_organization_user;
CREATE UNIQUE INDEX user_profiles_primary_per_user
    ON public.user_profiles(user_id)
    WHERE is_primary;

ALTER TABLE public.user_profiles
    DROP CONSTRAINT user_profiles_organization_membership_fkey;

ALTER TABLE public.organization_memberships
    DROP CONSTRAINT organization_memberships_organization_access_group_fkey,
    DROP CONSTRAINT organization_memberships_max_profiles_min_check,
    DROP CONSTRAINT organization_memberships_access_policy_revision_check,
    DROP COLUMN access_group_id,
    DROP COLUMN permissions,
    DROP COLUMN library_ids,
    DROP COLUMN max_playback_quality,
    DROP COLUMN max_streams,
    DROP COLUMN max_transcodes,
    DROP COLUMN transcode_allowed,
    DROP COLUMN audio_transcode_allowed,
    DROP COLUMN download_allowed,
    DROP COLUMN download_transcode_allowed,
    DROP COLUMN requests_allowed,
    DROP COLUMN max_profiles,
    DROP COLUMN access_policy_revision;

DROP TABLE public.membership_policy_rollback_snapshot;
DROP TABLE public.profile_primary_rollback_snapshot;
DROP TABLE public.legacy_user_policy_rollback_snapshot;
-- +goose StatementEnd
