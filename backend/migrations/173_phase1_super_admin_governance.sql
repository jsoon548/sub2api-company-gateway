-- Core Gateway governance: three roles, the singleton Super Administrator seat,
-- durable revocation epochs, and append-only governance evidence.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 0;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('super_admin', 'admin', 'user')) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_role_check;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'disabled')) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_status_check;

CREATE UNIQUE INDEX IF NOT EXISTS users_one_active_super_admin_idx
    ON users ((1))
    WHERE role = 'super_admin' AND status = 'active' AND deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS super_admin_seat (
    singleton_id SMALLINT PRIMARY KEY CHECK (singleton_id = 1),
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DO $$ BEGIN
    CREATE TYPE governance_actor_kind AS ENUM ('named_admin', 'deployment_operator', 'system');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE governance_result AS ENUM ('started', 'succeeded', 'failed', 'rejected', 'reconciled');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS governance_events (
    id UUID PRIMARY KEY,
    operation_id UUID NOT NULL,
    event_sequence INTEGER NOT NULL CHECK (event_sequence > 0),
    actor_kind governance_actor_kind NOT NULL,
    actor_user_id BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    deployment_operator_id VARCHAR(255),
    target_kind VARCHAR(64) NOT NULL,
    target_id VARCHAR(255) NOT NULL,
    action VARCHAR(64) NOT NULL,
    result governance_result NOT NULL,
    reason VARCHAR(512),
    before_summary JSONB,
    after_summary JSONB,
    recovery_nonce_fingerprint VARCHAR(64),
    gateway_request_id VARCHAR(36),
    safe_error_summary VARCHAR(512),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT governance_event_actor_attribution_check CHECK (
        (actor_kind = 'named_admin' AND actor_user_id IS NOT NULL AND deployment_operator_id IS NULL)
        OR (actor_kind = 'deployment_operator' AND actor_user_id IS NULL AND deployment_operator_id IS NOT NULL)
        OR (actor_kind = 'system' AND actor_user_id IS NULL AND deployment_operator_id IS NULL)
    ),
    UNIQUE (operation_id, event_sequence),
    UNIQUE (recovery_nonce_fingerprint)
);

CREATE INDEX IF NOT EXISTS governance_target_time_idx
    ON governance_events (target_kind, target_id, occurred_at);
CREATE INDEX IF NOT EXISTS governance_actor_time_idx
    ON governance_events (actor_user_id, occurred_at)
    WHERE actor_user_id IS NOT NULL;

CREATE OR REPLACE FUNCTION reject_governance_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'governance_events are append-only';
END;
$$;

DROP TRIGGER IF EXISTS governance_events_append_only ON governance_events;
CREATE TRIGGER governance_events_append_only
BEFORE UPDATE OR DELETE ON governance_events
FOR EACH ROW EXECUTE FUNCTION reject_governance_event_mutation();

-- Existing deployments select their oldest active administrator
-- deterministically. Empty deployments receive a seat atomically when setup
-- creates the first management account.
DO $$
DECLARE
    holder BIGINT;
    op UUID;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM super_admin_seat WHERE singleton_id = 1) THEN
        SELECT id INTO holder
        FROM users
        WHERE role IN ('super_admin', 'admin')
          AND status = 'active'
          AND deleted_at IS NULL
        ORDER BY CASE WHEN role = 'super_admin' THEN 0 ELSE 1 END, id
        LIMIT 1;

        IF holder IS NOT NULL THEN
            UPDATE users SET role = 'admin' WHERE role = 'super_admin' AND id <> holder;
            UPDATE users SET role = 'super_admin', status = 'active', deleted_at = NULL WHERE id = holder;
            INSERT INTO super_admin_seat(singleton_id, user_id, version, updated_at)
            VALUES (1, holder, 1, NOW());
            op := gen_random_uuid();
            INSERT INTO governance_events(
                id, operation_id, event_sequence, actor_kind, target_kind,
                target_id, action, result, reason, after_summary, occurred_at
            ) VALUES (
                gen_random_uuid(), op, 1, 'system', 'user', holder::text,
                'initialize_super_admin', 'succeeded', 'core_gateway_governance_migration',
                jsonb_build_object('role', 'super_admin', 'status', 'active'), NOW()
            );
        END IF;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_super_admin_seat()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    seat_count INTEGER;
BEGIN
    -- Empty deployments are valid until setup creates the first account.
    IF NOT EXISTS (SELECT 1 FROM users WHERE deleted_at IS NULL) THEN
        RETURN NULL;
    END IF;

    SELECT COUNT(*) INTO seat_count
    FROM super_admin_seat s
    JOIN users u ON u.id = s.user_id
    WHERE s.singleton_id = 1
      AND u.role = 'super_admin'
      AND u.status = 'active'
      AND u.deleted_at IS NULL;
    IF seat_count <> 1 THEN
        RAISE EXCEPTION 'exactly one active super_admin seat is required';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS users_super_admin_seat_guard ON users;
CREATE CONSTRAINT TRIGGER users_super_admin_seat_guard
AFTER INSERT OR UPDATE OF role, status, deleted_at OR DELETE ON users
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_super_admin_seat();

DROP TRIGGER IF EXISTS super_admin_seat_guard ON super_admin_seat;
CREATE CONSTRAINT TRIGGER super_admin_seat_guard
AFTER INSERT OR UPDATE OR DELETE ON super_admin_seat
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_super_admin_seat();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE deleted_at IS NULL)
       AND NOT EXISTS (
           SELECT 1
           FROM super_admin_seat s
           JOIN users u ON u.id = s.user_id
           WHERE s.singleton_id = 1
             AND u.role = 'super_admin'
             AND u.status = 'active'
             AND u.deleted_at IS NULL
       ) THEN
        RAISE EXCEPTION 'existing deployment has users but no eligible administrator for the super_admin seat';
    END IF;
END;
$$;
