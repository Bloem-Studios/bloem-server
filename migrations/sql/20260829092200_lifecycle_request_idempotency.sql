-- +goose Up

LOCK TABLE public.users IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE public.users ADD COLUMN account_incarnation_id uuid;
UPDATE public.users SET account_incarnation_id = gen_random_uuid();
ALTER TABLE public.users
    ALTER COLUMN account_incarnation_id SET DEFAULT gen_random_uuid(),
    ALTER COLUMN account_incarnation_id SET NOT NULL,
    ADD CONSTRAINT users_account_incarnation_id_key UNIQUE (account_incarnation_id);

-- +goose StatementBegin
CREATE FUNCTION public.guard_account_incarnation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        -- The database replaces even an explicitly supplied value, so callers
        -- cannot select an account incarnation.
        NEW.account_incarnation_id := gen_random_uuid();
        RETURN NEW;
    END IF;
    IF NEW.account_incarnation_id IS DISTINCT FROM OLD.account_incarnation_id THEN
        RAISE EXCEPTION 'account_incarnation_immutable' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER users_account_incarnation_guard
BEFORE INSERT OR UPDATE OF account_incarnation_id ON public.users
FOR EACH ROW EXECUTE FUNCTION public.guard_account_incarnation();

CREATE TABLE public.lifecycle_idempotency_control (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    phase text NOT NULL DEFAULT 'optional' CHECK (phase IN ('optional','required')),
    finalized_at timestamptz,
	finalized_route_digest bytea CHECK (finalized_route_digest IS NULL OR octet_length(finalized_route_digest) = 32),
	finalized_schema_digest bytea CHECK (finalized_schema_digest IS NULL OR octet_length(finalized_schema_digest) = 32),
    CONSTRAINT lifecycle_idempotency_control_finalized_check CHECK (
		(phase = 'optional' AND finalized_at IS NULL AND finalized_route_digest IS NULL AND finalized_schema_digest IS NULL)
		OR (phase = 'required' AND finalized_at IS NOT NULL AND finalized_route_digest IS NOT NULL AND finalized_schema_digest IS NOT NULL)
    )
);
INSERT INTO public.lifecycle_idempotency_control (singleton,phase) VALUES (true,'optional');

-- +goose StatementBegin
CREATE FUNCTION public.guard_lifecycle_idempotency_control() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (OLD.phase = 'required' AND NEW.phase <> 'required') THEN
        RAISE EXCEPTION 'lifecycle_idempotency_phase_irreversible' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.phase = 'required' AND OLD.phase = 'optional' THEN
        IF current_setting('bloem.lifecycle_idempotency_finalizer', true) IS DISTINCT FROM 'v1' THEN
            RAISE EXCEPTION 'lifecycle_idempotency_finalizer_required' USING ERRCODE = 'insufficient_privilege';
        END IF;
        PERFORM pg_advisory_xact_lock(hashtextextended('bloem.lifecycle_idempotency_handoff',0));
		IF (SELECT count(*) FROM public.lifecycle_idempotency_client_release_evidence) <> 3
		   OR EXISTS (
		       SELECT required.client FROM (VALUES ('web'),('apple'),('android')) AS required(client)
		       WHERE NOT EXISTS (
		           SELECT 1 FROM public.lifecycle_idempotency_client_release_evidence evidence
		           WHERE evidence.client = required.client
		       )
		   ) THEN
			RAISE EXCEPTION 'lifecycle_idempotency_client_evidence_incomplete' USING ERRCODE = 'check_violation';
		END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER lifecycle_idempotency_control_guard
BEFORE UPDATE OR DELETE ON public.lifecycle_idempotency_control
FOR EACH ROW EXECUTE FUNCTION public.guard_lifecycle_idempotency_control();

CREATE TABLE public.lifecycle_idempotency_client_release_evidence (
    client text PRIMARY KEY CHECK (client IN ('web','apple','android')),
    commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}([0-9a-f]{24})?$'),
    suite_digest bytea NOT NULL CHECK (octet_length(suite_digest) = 32),
    released_at timestamptz NOT NULL,
    release_channel_digest bytea NOT NULL CHECK (octet_length(release_channel_digest) = 32),
    recorded_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION public.guard_lifecycle_idempotency_client_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'lifecycle_idempotency_client_evidence_immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF current_setting('bloem.lifecycle_idempotency_evidence_writer', true) IS DISTINCT FROM 'v1' THEN
        RAISE EXCEPTION 'lifecycle_idempotency_evidence_writer_required' USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER lifecycle_idempotency_client_evidence_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.lifecycle_idempotency_client_release_evidence
FOR EACH ROW EXECUTE FUNCTION public.guard_lifecycle_idempotency_client_evidence();

CREATE TABLE public.lifecycle_request_receipts (
    idempotency_key_digest bytea PRIMARY KEY CHECK (octet_length(idempotency_key_digest) = 32),
    actor_kind text NOT NULL CHECK (actor_kind IN ('authenticated_account','preauth_intent')),
    actor_account_id integer,
    actor_account_incarnation_id uuid,
    actor_subject_digest bytea CHECK (actor_subject_digest IS NULL OR octet_length(actor_subject_digest) = 32),
    method text NOT NULL CHECK (method IN ('POST','PUT','PATCH','DELETE')),
    route_id text NOT NULL CHECK (route_id <> ''),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    target_source text CHECK (target_source IN ('path_account','path_tenant_member','body_account','stored_selection','exact_membership')),
    target_set_digest bytea CHECK (target_set_digest IS NULL OR octet_length(target_set_digest) = 32),
    operation_id text,
    state text NOT NULL CHECK (state IN ('binding_unresolved','bound','committed_pending','completed')),
    response_status integer CHECK (response_status BETWEEN 100 AND 599),
    response_body bytea,
    response_headers jsonb,
    failure_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT lifecycle_request_receipts_actor_shape_check CHECK (
        (actor_kind = 'authenticated_account' AND actor_account_id IS NOT NULL
            AND actor_account_incarnation_id IS NOT NULL AND actor_subject_digest IS NULL)
        OR (actor_kind = 'preauth_intent' AND actor_account_id IS NULL
            AND actor_account_incarnation_id IS NULL AND actor_subject_digest IS NOT NULL)
    ),
    CONSTRAINT lifecycle_request_receipts_binding_shape_check CHECK (
        (state = 'binding_unresolved' AND target_source IS NULL AND target_set_digest IS NULL)
        OR (state <> 'binding_unresolved' AND target_source IS NOT NULL AND target_set_digest IS NOT NULL)
    ),
    CONSTRAINT lifecycle_request_receipts_completion_shape_check CHECK (
        (state = 'completed' AND response_status IS NOT NULL AND completed_at IS NOT NULL)
        OR (state <> 'completed' AND completed_at IS NULL)
    )
);

CREATE INDEX lifecycle_request_receipts_actor_idx
    ON public.lifecycle_request_receipts (actor_account_incarnation_id,created_at DESC)
    WHERE actor_account_incarnation_id IS NOT NULL;
CREATE INDEX lifecycle_request_receipts_pending_idx
    ON public.lifecycle_request_receipts (created_at) WHERE state = 'committed_pending';

CREATE TABLE public.lifecycle_request_receipt_targets (
    idempotency_key_digest bytea NOT NULL,
    target_ordinal integer NOT NULL CHECK (target_ordinal >= 0),
    organization_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    account_id integer NOT NULL,
    account_incarnation_id uuid NOT NULL,
    profile_id text,
    resource_id text,
    PRIMARY KEY (idempotency_key_digest,target_ordinal),
    CONSTRAINT lifecycle_request_receipt_targets_receipt_fkey
        FOREIGN KEY (idempotency_key_digest)
        REFERENCES public.lifecycle_request_receipts(idempotency_key_digest) ON DELETE RESTRICT
);

-- +goose StatementBegin
CREATE FUNCTION public.guard_lifecycle_request_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_rank integer; new_rank integer;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_retained' USING ERRCODE = 'check_violation';
    END IF;
	IF OLD.state = 'completed' THEN
		RAISE EXCEPTION 'lifecycle_request_receipt_completed_immutable' USING ERRCODE = 'check_violation';
	END IF;
    IF NEW.idempotency_key_digest IS DISTINCT FROM OLD.idempotency_key_digest
       OR NEW.actor_kind IS DISTINCT FROM OLD.actor_kind
       OR NEW.actor_account_id IS DISTINCT FROM OLD.actor_account_id
       OR NEW.actor_account_incarnation_id IS DISTINCT FROM OLD.actor_account_incarnation_id
       OR NEW.actor_subject_digest IS DISTINCT FROM OLD.actor_subject_digest
       OR NEW.method IS DISTINCT FROM OLD.method OR NEW.route_id IS DISTINCT FROM OLD.route_id
       OR NEW.request_hash IS DISTINCT FROM OLD.request_hash
       OR (OLD.state <> 'binding_unresolved' AND NEW.target_source IS DISTINCT FROM OLD.target_source)
       OR (OLD.state <> 'binding_unresolved' AND NEW.target_set_digest IS DISTINCT FROM OLD.target_set_digest)
       OR (OLD.operation_id IS NOT NULL AND NEW.operation_id IS DISTINCT FROM OLD.operation_id) THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_binding_immutable' USING ERRCODE = 'check_violation';
    END IF;
    old_rank := CASE OLD.state WHEN 'binding_unresolved' THEN 0 WHEN 'bound' THEN 1 WHEN 'committed_pending' THEN 2 ELSE 3 END;
    new_rank := CASE NEW.state WHEN 'binding_unresolved' THEN 0 WHEN 'bound' THEN 1 WHEN 'committed_pending' THEN 2 ELSE 3 END;
    IF new_rank < old_rank OR new_rank > old_rank + 1 THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_state_nonmonotonic' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER lifecycle_request_receipt_guard
BEFORE UPDATE OR DELETE ON public.lifecycle_request_receipts
FOR EACH ROW EXECUTE FUNCTION public.guard_lifecycle_request_receipt();

-- +goose StatementBegin
CREATE FUNCTION public.guard_lifecycle_request_receipt_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF TG_OP = 'INSERT' AND NOT EXISTS (
		SELECT 1 FROM public.lifecycle_request_receipts receipt
		WHERE receipt.idempotency_key_digest = NEW.idempotency_key_digest
		  AND receipt.state IN ('binding_unresolved','bound')
	) THEN
		RAISE EXCEPTION 'lifecycle_request_receipt_target_closed' USING ERRCODE = 'check_violation';
	END IF;
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_target_immutable' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER lifecycle_request_receipt_target_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.lifecycle_request_receipt_targets
FOR EACH ROW EXECUTE FUNCTION public.guard_lifecycle_request_receipt_target();

-- +goose StatementBegin
CREATE FUNCTION public.reject_unresolved_lifecycle_request_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.lifecycle_request_receipts AS receipt
        WHERE receipt.idempotency_key_digest = NEW.idempotency_key_digest
          AND receipt.state = 'binding_unresolved'
    ) THEN
        RAISE EXCEPTION 'lifecycle_request_receipt_unresolved_at_commit' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER lifecycle_request_receipt_resolved_before_commit
AFTER INSERT OR UPDATE ON public.lifecycle_request_receipts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.reject_unresolved_lifecycle_request_receipt();

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.lifecycle_idempotency_control WHERE phase <> 'optional') THEN
        RAISE EXCEPTION 'lifecycle_idempotency_down_requires_optional_phase';
    END IF;
    IF EXISTS (SELECT 1 FROM public.lifecycle_request_receipts)
       OR EXISTS (SELECT 1 FROM public.lifecycle_idempotency_client_release_evidence) THEN
        RAISE EXCEPTION 'lifecycle_idempotency_down_requires_empty_history';
    END IF;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER lifecycle_request_receipt_resolved_before_commit ON public.lifecycle_request_receipts;
DROP FUNCTION public.reject_unresolved_lifecycle_request_receipt();
DROP TRIGGER lifecycle_request_receipt_target_guard ON public.lifecycle_request_receipt_targets;
DROP FUNCTION public.guard_lifecycle_request_receipt_target();
DROP TRIGGER lifecycle_request_receipt_guard ON public.lifecycle_request_receipts;
DROP FUNCTION public.guard_lifecycle_request_receipt();
DROP TABLE public.lifecycle_request_receipt_targets;
DROP TABLE public.lifecycle_request_receipts;
DROP TRIGGER lifecycle_idempotency_client_evidence_guard ON public.lifecycle_idempotency_client_release_evidence;
DROP FUNCTION public.guard_lifecycle_idempotency_client_evidence();
DROP TABLE public.lifecycle_idempotency_client_release_evidence;
DROP TRIGGER lifecycle_idempotency_control_guard ON public.lifecycle_idempotency_control;
DROP FUNCTION public.guard_lifecycle_idempotency_control();
DROP TABLE public.lifecycle_idempotency_control;
DROP TRIGGER users_account_incarnation_guard ON public.users;
DROP FUNCTION public.guard_account_incarnation();
ALTER TABLE public.users DROP CONSTRAINT users_account_incarnation_id_key;
ALTER TABLE public.users DROP COLUMN account_incarnation_id;
