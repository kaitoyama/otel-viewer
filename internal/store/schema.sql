-- otel-viewer DuckDB schema.
-- Applied idempotently at startup by store.Open.

-- runs.trace_id is logically unique. We avoid a PRIMARY KEY here so we can
-- freely UPDATE on it (the ART index that backs PK in DuckDB has known
-- limitations with UPDATE that produced spurious "duplicate key" errors).
-- Uniqueness is enforced by UpsertRun's DELETE-then-INSERT.
CREATE TABLE IF NOT EXISTS runs (
    trace_id        VARCHAR,
    name            VARCHAR,
    start_ns        BIGINT,
    end_ns          BIGINT,
    model           VARCHAR,
    vendor          VARCHAR,
    task_id         VARCHAR,
    task_source     VARCHAR,
    replay_of       VARCHAR,
    replay_group    VARCHAR,
    prompt_hash     VARCHAR,
    tags            VARCHAR,
    totals          VARCHAR,
    ingest_source   VARCHAR
);
CREATE INDEX IF NOT EXISTS runs_trace_id_idx ON runs(trace_id);
CREATE INDEX IF NOT EXISTS runs_task_id_idx ON runs(task_id);
CREATE INDEX IF NOT EXISTS runs_replay_group_idx ON runs(replay_group);
CREATE INDEX IF NOT EXISTS runs_prompt_hash_idx ON runs(prompt_hash);
CREATE INDEX IF NOT EXISTS runs_model_idx ON runs(model);
CREATE INDEX IF NOT EXISTS runs_start_idx ON runs(start_ns);

CREATE TABLE IF NOT EXISTS steps (
    trace_id        VARCHAR,
    span_id         VARCHAR,
    parent_span_id  VARCHAR,
    step_index      INTEGER,
    kind            VARCHAR,
    start_ns        BIGINT,
    duration_ns     BIGINT,
    agent_name      VARCHAR,
    tool_name       VARCHAR,
    operation       VARCHAR,
    model           VARCHAR,
    status_error    VARCHAR,
    payload         VARCHAR
);
CREATE INDEX IF NOT EXISTS steps_trace_idx ON steps(trace_id, step_index);
CREATE INDEX IF NOT EXISTS steps_tool_idx ON steps(tool_name);
CREATE INDEX IF NOT EXISTS steps_agent_idx ON steps(agent_name);
CREATE INDEX IF NOT EXISTS steps_model_idx ON steps(model);

CREATE TABLE IF NOT EXISTS events (
    trace_id        VARCHAR,
    span_id         VARCHAR,
    time_ns         BIGINT,
    name            VARCHAR,
    payload         VARCHAR
);
CREATE INDEX IF NOT EXISTS events_trace_idx ON events(trace_id);

CREATE TABLE IF NOT EXISTS feedback (
    id              VARCHAR PRIMARY KEY,
    trace_id        VARCHAR,
    span_id         VARCHAR,
    source          VARCHAR,
    score           DOUBLE,
    label           VARCHAR,
    comment         TEXT,
    created_at      TIMESTAMP
);
CREATE INDEX IF NOT EXISTS feedback_trace_idx ON feedback(trace_id);
CREATE INDEX IF NOT EXISTS feedback_score_idx ON feedback(score);

CREATE TABLE IF NOT EXISTS task_membership (
    task_id         VARCHAR,
    trace_id        VARCHAR,
    method          VARCHAR
);
CREATE INDEX IF NOT EXISTS task_membership_trace_idx ON task_membership(trace_id);
CREATE INDEX IF NOT EXISTS task_membership_task_idx ON task_membership(task_id);

CREATE TABLE IF NOT EXISTS replay_links (
    group_id        VARCHAR,
    trace_id        VARCHAR,
    role            VARCHAR
);
CREATE INDEX IF NOT EXISTS replay_links_trace_idx ON replay_links(trace_id);
CREATE INDEX IF NOT EXISTS replay_links_group_idx ON replay_links(group_id);

CREATE TABLE IF NOT EXISTS tempo_backfill_jobs (
    id              VARCHAR PRIMARY KEY,
    base_url        VARCHAR,
    traceql         VARCHAR,
    start_ns        BIGINT,
    end_ns          BIGINT,
    status          VARCHAR,
    fetched         INTEGER,
    total           INTEGER,
    started_at      TIMESTAMP,
    finished_at     TIMESTAMP,
    error           TEXT
);
