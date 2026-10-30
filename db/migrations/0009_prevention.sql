-- Migration 0009: immutable local prevention metadata and scoped deletion.
-- Artifact bodies and repository absolute paths never enter PostgreSQL.

CREATE TABLE prevention_artifacts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_key text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('regression_test', 'deterministic_check', 'agent_instruction', 'intent')),
    title       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    source      text NOT NULL,
    actor       text NOT NULL
);

CREATE TABLE prevention_artifact_versions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id     uuid NOT NULL REFERENCES prevention_artifacts (id) ON DELETE CASCADE,
    version         integer NOT NULL CHECK (version >= 1),
    artifact_path   text NOT NULL,
    content_sha256  text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    status          text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'verified', 'active', 'deprecated')),
    commit_sha      text CHECK (commit_sha IS NULL OR commit_sha ~ '^[0-9a-f]{40}$'),
    activated_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    source          text NOT NULL,
    actor           text NOT NULL,
    UNIQUE (artifact_id, version),
    CHECK (
        (status = 'active' AND commit_sha IS NOT NULL AND activated_at IS NOT NULL)
        OR (status IN ('draft', 'verified') AND commit_sha IS NULL AND activated_at IS NULL)
        OR status = 'deprecated'
    )
);

CREATE UNIQUE INDEX prevention_one_active_version
    ON prevention_artifact_versions (artifact_id) WHERE status = 'active';
CREATE INDEX prevention_versions_artifact_idx
    ON prevention_artifact_versions (artifact_id, version);
CREATE INDEX prevention_versions_activated_idx
    ON prevention_artifact_versions (activated_at) WHERE activated_at IS NOT NULL;

CREATE TABLE issue_preventions (
    issue_id    uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    artifact_id uuid NOT NULL REFERENCES prevention_artifacts (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    source      text NOT NULL,
    actor       text NOT NULL,
    PRIMARY KEY (issue_id, artifact_id)
);

CREATE INDEX issue_preventions_artifact_idx ON issue_preventions (artifact_id, issue_id);

CREATE TABLE prevention_verifications (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_version_id uuid NOT NULL REFERENCES prevention_artifact_versions (id) ON DELETE CASCADE,
    command             text NOT NULL,
    exit_code           integer,
    passed              boolean NOT NULL,
    duration_ms         bigint NOT NULL CHECK (duration_ms >= 0),
    evidence            text NOT NULL,
    environment         jsonb NOT NULL DEFAULT '{}'::jsonb,
    content_sha256      text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    created_at          timestamptz NOT NULL DEFAULT now(),
    source              text NOT NULL,
    actor               text NOT NULL
);

CREATE INDEX prevention_verifications_version_created_idx
    ON prevention_verifications (artifact_version_id, created_at DESC);
CREATE INDEX occurrences_issue_occurred_idx
    ON occurrences (issue_id, occurred_at);

CREATE FUNCTION errkb_prevention_version_immutable() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.artifact_id IS DISTINCT FROM OLD.artifact_id
       OR NEW.version IS DISTINCT FROM OLD.version
       OR NEW.artifact_path IS DISTINCT FROM OLD.artifact_path
       OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'prevention artifact identity, content hash and creation time are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER prevention_version_immutable
    BEFORE UPDATE ON prevention_artifact_versions
    FOR EACH ROW EXECUTE FUNCTION errkb_prevention_version_immutable();

CREATE OR REPLACE FUNCTION errkb_delete_preview(
    p_project_key text DEFAULT NULL,
    p_connector_id uuid DEFAULT NULL,
    p_before timestamptz DEFAULT NULL
) RETURNS TABLE (kind text, rows bigint)
LANGUAGE plpgsql
STABLE
AS $$
BEGIN
    IF p_project_key IS NULL AND p_connector_id IS NULL AND p_before IS NULL THEN
        RAISE EXCEPTION 'at least one deletion filter (project, connector or before) is required'
            USING ERRCODE = '22023';
    END IF;

    RETURN QUERY
    WITH target_issues AS (
        SELECT i.id FROM issues i
        WHERE p_project_key IS NOT NULL AND i.project_key = p_project_key
    ), target_events AS (
        SELECT e.event_id FROM events e
        WHERE (p_project_key IS NULL OR e.project_key = p_project_key)
          AND (p_connector_id IS NULL OR e.connector_id = p_connector_id)
          AND (p_before IS NULL OR e.observed_at < p_before)
    ), target_occurrences AS (
        SELECT o.id FROM occurrences o
        WHERE o.issue_id IN (SELECT id FROM target_issues)
           OR o.event_id IN (SELECT event_id FROM target_events)
           OR (p_connector_id IS NOT NULL AND o.connector_id = p_connector_id
               AND (p_before IS NULL OR o.occurred_at < p_before))
           OR (p_before IS NOT NULL AND p_project_key IS NULL AND p_connector_id IS NULL
               AND o.occurred_at < p_before)
    ), target_solutions AS (
        SELECT s.id FROM solutions s WHERE s.issue_id IN (SELECT id FROM target_issues)
    ), target_artifacts AS (
        SELECT a.id FROM prevention_artifacts a
        WHERE p_project_key IS NOT NULL AND a.project_key = p_project_key
    ), target_versions AS (
        SELECT v.id FROM prevention_artifact_versions v
        WHERE v.artifact_id IN (SELECT id FROM target_artifacts)
    )
    SELECT 'prevention_verifications', count(*) FROM prevention_verifications r
        WHERE r.artifact_version_id IN (SELECT id FROM target_versions)
    UNION ALL
    SELECT 'issue_preventions', count(*) FROM issue_preventions p
        WHERE p.artifact_id IN (SELECT id FROM target_artifacts)
           OR p.issue_id IN (SELECT id FROM target_issues)
    UNION ALL
    SELECT 'prevention_artifact_versions', count(*) FROM target_versions
    UNION ALL
    SELECT 'prevention_artifacts', count(*) FROM target_artifacts
    UNION ALL
    SELECT 'issues', count(*) FROM target_issues
    UNION ALL
    SELECT 'solutions', count(*) FROM target_solutions
    UNION ALL
    SELECT 'solution_versions', count(*) FROM solution_versions v
        WHERE v.solution_id IN (SELECT id FROM target_solutions)
    UNION ALL
    SELECT 'outcomes', count(*) FROM outcomes u
        WHERE u.issue_id IN (SELECT id FROM target_issues)
           OR u.occurrence_id IN (SELECT id FROM target_occurrences)
           OR (p_before IS NOT NULL AND u.observed_at < p_before)
    UNION ALL
    SELECT 'retrievals', count(*) FROM retrievals r
        WHERE r.candidate_issue_id IN (SELECT id FROM target_issues)
           OR (p_before IS NOT NULL AND r.created_at < p_before)
    UNION ALL
    SELECT 'notices', count(*) FROM notices n
        WHERE n.issue_id IN (SELECT id FROM target_issues)
           OR (p_before IS NOT NULL AND n.created_at < p_before)
    UNION ALL
    SELECT 'occurrences', count(*) FROM target_occurrences
    UNION ALL
    SELECT 'events', count(*) FROM target_events
    UNION ALL
    SELECT 'audit_log', 0::bigint;
END $$;

CREATE OR REPLACE FUNCTION errkb_delete_scope(
    p_project_key text DEFAULT NULL,
    p_connector_id uuid DEFAULT NULL,
    p_before timestamptz DEFAULT NULL,
    p_actor text DEFAULT 'owner'
) RETURNS TABLE (kind text, removed bigint)
LANGUAGE plpgsql
AS $$
DECLARE
    v_issue_ids         uuid[] := '{}';
    v_event_ids         uuid[] := '{}';
    v_occurrence_ids    uuid[] := '{}';
    v_solution_ids      uuid[] := '{}';
    v_artifact_ids      uuid[] := '{}';
    v_version_ids       uuid[] := '{}';
    v_prevention_verifications bigint := 0;
    v_issue_preventions bigint := 0;
    v_prevention_versions bigint := 0;
    v_prevention_artifacts bigint := 0;
    v_issues            bigint := 0;
    v_solutions         bigint := 0;
    v_solution_versions bigint := 0;
    v_outcomes          bigint := 0;
    v_retrievals        bigint := 0;
    v_notices           bigint := 0;
    v_occurrences       bigint := 0;
    v_events            bigint := 0;
BEGIN
    IF p_project_key IS NULL AND p_connector_id IS NULL AND p_before IS NULL THEN
        RAISE EXCEPTION 'at least one deletion filter (project, connector or before) is required'
            USING ERRCODE = '22023';
    END IF;
    IF p_actor IS NULL OR trim(p_actor) = '' THEN
        RAISE EXCEPTION 'an actor is required for a deletion' USING ERRCODE = '22023';
    END IF;

    SELECT coalesce(array_agg(i.id), '{}') INTO v_issue_ids
      FROM issues i WHERE p_project_key IS NOT NULL AND i.project_key = p_project_key;
    SELECT coalesce(array_agg(a.id), '{}') INTO v_artifact_ids
      FROM prevention_artifacts a
     WHERE p_project_key IS NOT NULL AND a.project_key = p_project_key;
    SELECT coalesce(array_agg(v.id), '{}') INTO v_version_ids
      FROM prevention_artifact_versions v WHERE v.artifact_id = any(v_artifact_ids);

    DELETE FROM prevention_verifications r WHERE r.artifact_version_id = any(v_version_ids);
    GET DIAGNOSTICS v_prevention_verifications = ROW_COUNT;
    DELETE FROM issue_preventions p
     WHERE p.artifact_id = any(v_artifact_ids) OR p.issue_id = any(v_issue_ids);
    GET DIAGNOSTICS v_issue_preventions = ROW_COUNT;
    DELETE FROM prevention_artifact_versions v WHERE v.id = any(v_version_ids);
    GET DIAGNOSTICS v_prevention_versions = ROW_COUNT;
    DELETE FROM prevention_artifacts a WHERE a.id = any(v_artifact_ids);
    GET DIAGNOSTICS v_prevention_artifacts = ROW_COUNT;
    SELECT coalesce(array_agg(e.event_id), '{}') INTO v_event_ids
      FROM events e
     WHERE (p_project_key IS NULL OR e.project_key = p_project_key)
       AND (p_connector_id IS NULL OR e.connector_id = p_connector_id)
       AND (p_before IS NULL OR e.observed_at < p_before);
    SELECT coalesce(array_agg(o.id), '{}') INTO v_occurrence_ids
      FROM occurrences o
     WHERE o.issue_id = any(v_issue_ids)
        OR o.event_id = any(v_event_ids)
        OR (p_connector_id IS NOT NULL AND o.connector_id = p_connector_id
            AND (p_before IS NULL OR o.occurred_at < p_before))
        OR (p_before IS NOT NULL AND p_project_key IS NULL AND p_connector_id IS NULL
            AND o.occurred_at < p_before);
    SELECT coalesce(array_agg(s.id), '{}') INTO v_solution_ids
      FROM solutions s WHERE s.issue_id = any(v_issue_ids);

    DELETE FROM outcomes u
     WHERE u.issue_id = any(v_issue_ids)
        OR u.occurrence_id = any(v_occurrence_ids)
        OR (p_before IS NOT NULL AND u.observed_at < p_before);
    GET DIAGNOSTICS v_outcomes = ROW_COUNT;
    DELETE FROM retrievals r
     WHERE r.candidate_issue_id = any(v_issue_ids)
        OR (p_before IS NOT NULL AND r.created_at < p_before);
    GET DIAGNOSTICS v_retrievals = ROW_COUNT;
    DELETE FROM notices n
     WHERE n.issue_id = any(v_issue_ids)
        OR (p_before IS NOT NULL AND n.created_at < p_before);
    GET DIAGNOSTICS v_notices = ROW_COUNT;
    DELETE FROM occurrences o WHERE o.id = any(v_occurrence_ids);
    GET DIAGNOSTICS v_occurrences = ROW_COUNT;
    DELETE FROM solution_versions v WHERE v.solution_id = any(v_solution_ids);
    GET DIAGNOSTICS v_solution_versions = ROW_COUNT;
    DELETE FROM solutions s WHERE s.id = any(v_solution_ids);
    GET DIAGNOSTICS v_solutions = ROW_COUNT;
    DELETE FROM events e WHERE e.event_id = any(v_event_ids);
    GET DIAGNOSTICS v_events = ROW_COUNT;
    DELETE FROM issues i WHERE i.id = any(v_issue_ids);
    GET DIAGNOSTICS v_issues = ROW_COUNT;

    INSERT INTO audit_log (action, entity, entity_id, operator, source, detail)
    VALUES ('delete.scope', 'scope',
            coalesce(p_project_key, '') || '|' || coalesce(p_connector_id::text, ''),
            p_actor, 'user',
            jsonb_build_object(
                'project_key', p_project_key,
                'connector_id', p_connector_id,
                'before', p_before,
                'counts', jsonb_build_object(
                    'prevention_verifications', v_prevention_verifications,
                    'issue_preventions', v_issue_preventions,
                    'prevention_artifact_versions', v_prevention_versions,
                    'prevention_artifacts', v_prevention_artifacts,
                    'issues', v_issues,
                    'solutions', v_solutions,
                    'solution_versions', v_solution_versions,
                    'outcomes', v_outcomes,
                    'retrievals', v_retrievals,
                    'notices', v_notices,
                    'occurrences', v_occurrences,
                    'events', v_events)));

    RETURN QUERY VALUES
        ('prevention_verifications', v_prevention_verifications),
        ('issue_preventions', v_issue_preventions),
        ('prevention_artifact_versions', v_prevention_versions),
        ('prevention_artifacts', v_prevention_artifacts),
        ('issues', v_issues),
        ('solutions', v_solutions),
        ('solution_versions', v_solution_versions),
        ('outcomes', v_outcomes),
        ('retrievals', v_retrievals),
        ('notices', v_notices),
        ('occurrences', v_occurrences),
        ('events', v_events);
END $$;
