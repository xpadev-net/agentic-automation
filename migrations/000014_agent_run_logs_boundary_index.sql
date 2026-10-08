-- Migration: 000014_agent_run_logs_boundary_index
-- Description: Index (agent_run_id, line prefix) so the ingestion handler's
--   PEM-boundary sentinel lookup stays O(#sentinels) instead of rescanning
--   every stored row of the attempt on each batch.
-- Created: 2026-10-08

-- +goose Up

-- The sentinel replay query filters on (agent_run_id, line = <sentinel>) where
-- the sentinel strings are ~30 chars; a 32-char prefix index covers them while
-- keeping the index small for MEDIUMTEXT rows.
CREATE INDEX idx_agent_run_logs_boundary ON agent_run_logs (agent_run_id, line(32));

-- +goose Down

DROP INDEX idx_agent_run_logs_boundary ON agent_run_logs;
