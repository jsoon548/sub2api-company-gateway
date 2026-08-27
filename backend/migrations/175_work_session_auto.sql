-- Reliable Work Session identity, Auto configuration, synchronous complexity
-- routing, authoritative Route Decisions, and bounded Gateway Internal
-- Inference run facts. Migration 176 remains reserved for pricing and quota.

CREATE TABLE work_sessions (
    id UUID PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    employee_user_id BIGINT NOT NULL,
    profile_version VARCHAR(64) NOT NULL,
    signal_source VARCHAR(64) NOT NULL,
    signal_status VARCHAR(16) NOT NULL,
    session_key_hmac BYTEA NULL,
    hmac_key_version VARCHAR(64) NULL,
    reliability VARCHAR(16) NOT NULL,
    routing_mode VARCHAR(16) NOT NULL,
    config_version BIGINT NOT NULL,
    analysis_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    quota_grace_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    selected_logical_model VARCHAR(100) NULL,
    selected_tier VARCHAR(16) NULL,
    selected_complexity VARCHAR(16) NULL,
    required_capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    routing_version BIGINT NOT NULL DEFAULT 0,
    first_gateway_request_id UUID NOT NULL,
    last_gateway_request_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT work_sessions_employee_fk
        FOREIGN KEY (employee_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT work_sessions_signal_status_check
        CHECK (signal_status IN ('verified', 'missing', 'malformed')),
    CONSTRAINT work_sessions_reliability_check
        CHECK (reliability IN ('reliable', 'unreliable')),
    CONSTRAINT work_sessions_routing_mode_check
        CHECK (routing_mode IN ('explicit', 'auto')),
    CONSTRAINT work_sessions_status_check
        CHECK (status IN ('active', 'request_scoped')),
    CONSTRAINT work_sessions_selected_tier_check
        CHECK (selected_tier IS NULL OR selected_tier IN ('economy', 'general', 'advanced')),
    CONSTRAINT work_sessions_selected_complexity_check
        CHECK (selected_complexity IS NULL OR selected_complexity IN ('simple', 'general', 'complex')),
    CONSTRAINT work_sessions_required_capabilities_check
        CHECK (jsonb_typeof(required_capabilities) = 'array'),
    CONSTRAINT work_sessions_routing_version_check
        CHECK (routing_version >= 0),
    CONSTRAINT work_sessions_route_state_check
        CHECK (
            (routing_version = 0
             AND selected_logical_model IS NULL
             AND selected_tier IS NULL
             AND selected_complexity IS NULL
             AND required_capabilities = '[]'::jsonb)
            OR
            (routing_version > 0
             AND reliability = 'reliable'
             AND selected_logical_model IS NOT NULL
             AND selected_tier IS NOT NULL
             AND selected_complexity IS NOT NULL)
        ),
    CONSTRAINT work_sessions_config_version_check
        CHECK (config_version > 0),
    CONSTRAINT work_sessions_key_shape_check
        CHECK (
            (reliability = 'reliable'
             AND signal_status = 'verified'
             AND session_key_hmac IS NOT NULL
             AND octet_length(session_key_hmac) = 32
             AND hmac_key_version IS NOT NULL
             AND length(hmac_key_version) > 0
             AND analysis_eligible
             AND status = 'active')
            OR
            (reliability = 'unreliable'
             AND signal_status IN ('missing', 'malformed')
             AND session_key_hmac IS NULL
             AND hmac_key_version IS NULL
             AND NOT analysis_eligible
             AND NOT quota_grace_eligible
             AND status = 'request_scoped')
        ),
    CONSTRAINT work_sessions_activity_time_check
        CHECK (last_activity_at >= created_at)
);

CREATE UNIQUE INDEX work_sessions_reliable_identity_key
    ON work_sessions(
        tenant_id,
        employee_user_id,
        profile_version,
        signal_source,
        hmac_key_version,
        session_key_hmac
    )
    WHERE reliability = 'reliable';

CREATE INDEX work_sessions_employee_activity_idx
    ON work_sessions(employee_user_id, last_activity_at DESC);

CREATE INDEX work_sessions_config_version_idx
    ON work_sessions(config_version, created_at DESC);

CREATE TABLE model_catalog_entries (
    id UUID PRIMARY KEY,
    generation BIGINT NOT NULL,
    logical_model VARCHAR(100) NOT NULL,
    provider_model VARCHAR(100) NOT NULL,
    tier VARCHAR(16) NOT NULL,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NULL,
    emergency_disabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_catalog_generation_check CHECK (generation > 0),
    CONSTRAINT model_catalog_tier_check CHECK (tier IN ('economy', 'general', 'advanced')),
    CONSTRAINT model_catalog_capabilities_check CHECK (jsonb_typeof(capabilities) = 'array'),
    CONSTRAINT model_catalog_validity_check CHECK (valid_until IS NULL OR valid_until > valid_from),
    CONSTRAINT model_catalog_generation_model_key UNIQUE (generation, logical_model)
);

CREATE INDEX model_catalog_generation_tier_idx
    ON model_catalog_entries(generation, tier, logical_model);

CREATE INDEX model_catalog_emergency_idx
    ON model_catalog_entries(logical_model)
    WHERE emergency_disabled;

CREATE TABLE auto_candidate_pools (
    id UUID PRIMARY KEY,
    generation BIGINT NOT NULL,
    tier VARCHAR(16) NOT NULL,
    position INTEGER NOT NULL,
    catalog_entry_id UUID NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT auto_candidate_pool_catalog_fk
        FOREIGN KEY (catalog_entry_id) REFERENCES model_catalog_entries(id) ON DELETE RESTRICT,
    CONSTRAINT auto_candidate_pool_generation_check CHECK (generation > 0),
    CONSTRAINT auto_candidate_pool_tier_check CHECK (tier IN ('economy', 'general', 'advanced')),
    CONSTRAINT auto_candidate_pool_position_check CHECK (position > 0),
    CONSTRAINT auto_candidate_pool_validity_check CHECK (valid_until IS NULL OR valid_until > valid_from),
    CONSTRAINT auto_candidate_pool_position_key UNIQUE (generation, tier, position),
    CONSTRAINT auto_candidate_pool_entry_key UNIQUE (generation, tier, catalog_entry_id)
);

ALTER TABLE audit_interactions
    ADD COLUMN work_session_id UUID NULL;

ALTER TABLE audit_interactions
    ADD CONSTRAINT audit_interactions_work_session_fk
    FOREIGN KEY (work_session_id) REFERENCES work_sessions(id) ON DELETE RESTRICT;

CREATE INDEX audit_interactions_work_session_idx
    ON audit_interactions(work_session_id, admitted_at DESC)
    WHERE work_session_id IS NOT NULL;

CREATE TABLE gateway_inference_runs (
    id UUID PRIMARY KEY,
    purpose VARCHAR(64) NOT NULL,
    profile VARCHAR(64) NOT NULL,
    backend VARCHAR(100) NOT NULL,
    provider VARCHAR(100) NOT NULL,
    model VARCHAR(100) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    schema_version VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    provider_request_id VARCHAR(255) NULL,
    input_units BIGINT NULL,
    output_units BIGINT NULL,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT gateway_inference_runs_text_check CHECK (
        length(purpose) > 0 AND length(profile) > 0 AND length(backend) > 0
        AND length(provider) > 0 AND length(model) > 0
        AND length(prompt_version) > 0 AND length(schema_version) > 0
    ),
    CONSTRAINT gateway_inference_runs_status_check CHECK (status IN (
        'completed','invalid_request','unavailable','timeout','canceled','rejected',
        'rate_limited','provider_error','refused','empty_response','invalid_response',
        'response_too_large','usage_missing'
    )),
    CONSTRAINT gateway_inference_runs_units_check CHECK (
        (input_units IS NULL OR input_units >= 0)
        AND (output_units IS NULL OR output_units >= 0)
        AND latency_ms >= 0
    )
);

CREATE INDEX gateway_inference_runs_profile_created_idx
    ON gateway_inference_runs(profile, created_at DESC);

CREATE INDEX gateway_inference_runs_status_created_idx
    ON gateway_inference_runs(status, created_at DESC);

CREATE TABLE route_decisions (
    id UUID PRIMARY KEY,
    gateway_request_id UUID NOT NULL UNIQUE,
    work_session_id UUID NOT NULL,
    employee_user_id BIGINT NOT NULL,
    profile_version VARCHAR(64) NOT NULL,
    config_version BIGINT NOT NULL,
    required_capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    task_complexity VARCHAR(16) NOT NULL,
    certainty VARCHAR(16) NOT NULL,
    explanation VARCHAR(1024) NOT NULL,
    decision_source VARCHAR(16) NOT NULL,
    rule_version VARCHAR(64) NOT NULL,
    classifier_run_id UUID NULL UNIQUE,
    classifier_version VARCHAR(64) NULL,
    classifier_status VARCHAR(32) NOT NULL,
    classifier_latency_ms BIGINT NOT NULL DEFAULT 0,
    requested_tier VARCHAR(16) NOT NULL,
    effective_tier VARCHAR(16) NOT NULL,
    candidate_pool JSONB NOT NULL DEFAULT '[]'::jsonb,
    actual_logical_model VARCHAR(100) NULL,
    actual_provider_model VARCHAR(100) NULL,
    change_reason VARCHAR(64) NOT NULL,
    technical_retry_count SMALLINT NOT NULL DEFAULT 0,
    technical_retry_reason VARCHAR(64) NULL,
    decision_result VARCHAR(16) NOT NULL,
    routing_latency_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT route_decisions_gateway_request_fk
        FOREIGN KEY (gateway_request_id) REFERENCES audit_interactions(gateway_request_id) ON DELETE RESTRICT,
    CONSTRAINT route_decisions_work_session_fk
        FOREIGN KEY (work_session_id) REFERENCES work_sessions(id) ON DELETE RESTRICT,
    CONSTRAINT route_decisions_employee_fk
        FOREIGN KEY (employee_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT route_decisions_classifier_run_fk
        FOREIGN KEY (classifier_run_id) REFERENCES gateway_inference_runs(id) ON DELETE RESTRICT,
    CONSTRAINT route_decisions_config_version_check CHECK (config_version > 0),
    CONSTRAINT route_decisions_required_capabilities_check CHECK (jsonb_typeof(required_capabilities) = 'array'),
    CONSTRAINT route_decisions_complexity_check CHECK (task_complexity IN ('simple', 'general', 'complex')),
    CONSTRAINT route_decisions_certainty_check CHECK (certainty IN ('deterministic', 'decisive', 'uncertain')),
    CONSTRAINT route_decisions_source_check CHECK (decision_source IN ('rule', 'classifier', 'fallback')),
    CONSTRAINT route_decisions_classifier_status_check
        CHECK (classifier_status IN ('not_called', 'completed', 'timeout', 'invalid', 'unavailable')),
    CONSTRAINT route_decisions_classifier_run_check
        CHECK (classifier_run_id IS NULL OR classifier_status <> 'not_called'),
    CONSTRAINT route_decisions_requested_tier_check CHECK (requested_tier IN ('economy', 'general', 'advanced')),
    CONSTRAINT route_decisions_effective_tier_check CHECK (effective_tier IN ('economy', 'general', 'advanced')),
    CONSTRAINT route_decisions_candidate_pool_check CHECK (jsonb_typeof(candidate_pool) = 'array'),
    CONSTRAINT route_decisions_retry_count_check CHECK (technical_retry_count BETWEEN 0 AND 1),
    CONSTRAINT route_decisions_result_check CHECK (decision_result IN ('selected', 'unavailable', 'failed')),
    CONSTRAINT route_decisions_latency_check CHECK (classifier_latency_ms >= 0 AND routing_latency_ms >= 0),
    CONSTRAINT route_decisions_selected_model_check CHECK (
        (decision_result = 'selected' AND actual_logical_model IS NOT NULL AND actual_provider_model IS NOT NULL)
        OR
        (decision_result <> 'selected' AND actual_logical_model IS NULL AND actual_provider_model IS NULL)
    )
);

CREATE INDEX route_decisions_session_created_idx
    ON route_decisions(work_session_id, created_at DESC);

CREATE INDEX route_decisions_employee_created_idx
    ON route_decisions(employee_user_id, created_at DESC);

CREATE INDEX route_decisions_classifier_latency_idx
    ON route_decisions(classifier_status, classifier_latency_ms)
    WHERE classifier_status <> 'not_called';
