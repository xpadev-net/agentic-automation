-- Migration: 000013_add_agent_run_logs
-- Description: Add agent_run_logs table for persisted runner log lines (WebUI)
-- Created: 2026-10-07

-- +goose Up

-- Table: agent_run_logs
-- Runner pods push their formatted stderr lines in batches; each line keeps a
-- per-run monotonically increasing seq for ordering and gap detection.
CREATE TABLE IF NOT EXISTS agent_run_logs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    agent_run_id INT NOT NULL,
    seq BIGINT NOT NULL COMMENT 'Per-run monotonic sequence assigned by runner',
    ts DATETIME(3) NULL COMMENT 'Runner-side line timestamp',
    line MEDIUMTEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE KEY uq_agent_run_log_seq (agent_run_id, seq),
    INDEX idx_agent_run_logs_run (agent_run_id, id),
    INDEX idx_agent_run_logs_created (created_at),
    FOREIGN KEY fk_agent_run_logs_run (agent_run_id) REFERENCES agent_runs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down

DROP TABLE IF EXISTS agent_run_logs;
