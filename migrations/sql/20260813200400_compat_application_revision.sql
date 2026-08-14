-- +goose Up
-- +goose StatementBegin
-- Optimistic concurrency for the Compatibility Applications admin surface.
--
-- The surface is read-and-decide: an administrator loads the application
-- list, looks at it, and sends the revision they were looking at back with
-- every enable, disable, rotate, or revoke. Two administrators acting on the
-- same stale page must not both win, so the revision is derived in the
-- database rather than assigned by a writer. The BEFORE UPDATE guard bumps it
-- exactly when a governed column changes and forces it back to its previous
-- value otherwise, so a writer that bypasses the service can neither skip the
-- bump nor advance the revision on a write that decided nothing.
--
-- "Governed" is the administrative decision surface: enablement, revocation,
-- the capability grant, the bound client certificate, the registered API
-- range, and administrator-forced credential rotation. It deliberately
-- excludes companion liveness (health_status, last_contact_at) and companion
-- self-renewal, which move on their own every heartbeat and every credential
-- window. Folding those in would expire an administrator's expected revision
-- with no decision having been taken, and the guard would produce nothing but
-- spurious conflicts.

ALTER TABLE public.compat_applications
    -- Monotonic per row: existing rows start at 1, and the guard below is the
    -- only thing that ever advances it.
    ADD COLUMN revision bigint NOT NULL DEFAULT 1,
    -- When an administrator last forced a credential rotation. Companion
    -- self-renewal does not touch it: renewal is liveness, not a decision.
    -- Its presence is what makes rotation a governed change, so two
    -- administrators cannot both rotate from the same page and silently kill
    -- each other's freshly issued credential.
    ADD COLUMN credential_rotated_at timestamptz,
    -- Nullness is stated explicitly: a CHECK passes when its predicate
    -- evaluates to NULL, so `revision >= 1` alone would admit a NULL.
    ADD CONSTRAINT compat_applications_revision_check
        CHECK (revision IS NOT NULL AND revision >= 1),
    -- The admin surface addresses an application by instance_id alone
    -- (/applications/{instance_id}/disable), and the identifier is chosen by
    -- the enrolling companion. Kind-scoped uniqueness would let a second
    -- companion claim the first one's identifier and leave every
    -- administrative control ambiguous for both, so the identifier is a
    -- global address. That strictly implies the kind-scoped constraint it
    -- replaces.
    ADD CONSTRAINT compat_applications_instance_id_key UNIQUE (instance_id),
    DROP CONSTRAINT compat_applications_instance_key;

-- Replaces the identity guard with one that also derives the revision. It
-- stays a single BEFORE UPDATE trigger so there is no ordering question
-- between an identity check and a revision bump.
CREATE OR REPLACE FUNCTION public.vondel_compat_application_identity_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    governed_change boolean;
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.kind IS DISTINCT FROM OLD.kind
       OR NEW.instance_id IS DISTINCT FROM OLD.instance_id THEN
        RAISE EXCEPTION 'compat application identity is immutable';
    END IF;

    governed_change :=
        NEW.enabled IS DISTINCT FROM OLD.enabled
        OR NEW.revoked_at IS DISTINCT FROM OLD.revoked_at
        OR NEW.credential_rotated_at IS DISTINCT FROM OLD.credential_rotated_at
        OR NEW.granted_capabilities IS DISTINCT FROM OLD.granted_capabilities
        OR NEW.tls_fingerprint IS DISTINCT FROM OLD.tls_fingerprint
        OR NEW.api_range_min IS DISTINCT FROM OLD.api_range_min
        OR NEW.api_range_max IS DISTINCT FROM OLD.api_range_max
        OR NEW.version IS DISTINCT FROM OLD.version
        OR NEW.image_digest IS DISTINCT FROM OLD.image_digest;

    -- Assigned on both branches: the revision is derived state, so whatever
    -- the writer supplied is discarded either way.
    IF governed_change THEN
        NEW.revision := OLD.revision + 1;
    ELSE
        NEW.revision := OLD.revision;
    END IF;

    NEW.updated_at := now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.vondel_compat_application_identity_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.kind IS DISTINCT FROM OLD.kind
       OR NEW.instance_id IS DISTINCT FROM OLD.instance_id THEN
        RAISE EXCEPTION 'compat application identity is immutable';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

ALTER TABLE public.compat_applications
    ADD CONSTRAINT compat_applications_instance_key UNIQUE (kind, instance_id),
    DROP CONSTRAINT IF EXISTS compat_applications_instance_id_key,
    DROP CONSTRAINT IF EXISTS compat_applications_revision_check,
    DROP COLUMN IF EXISTS credential_rotated_at,
    DROP COLUMN IF EXISTS revision;
-- +goose StatementEnd
