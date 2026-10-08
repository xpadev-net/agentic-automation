-- Migration: 000014_agent_run_logs_redact_state
-- Description: Persist per-line redaction state (PEM block + credential
--   stream) on agent_run_logs so ingestion batches resume masking exactly
--   where the preceding stored row left off — the authoritative replay,
--   replacing sentinel boundary lookups.
-- Created: 2026-10-08

-- +goose Up

ALTER TABLE agent_run_logs
    ADD COLUMN redact_state VARCHAR(32) NOT NULL DEFAULT '' AFTER line;

-- +goose Down

ALTER TABLE agent_run_logs
    DROP COLUMN redact_state;
