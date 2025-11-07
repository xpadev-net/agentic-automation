-- Migration: 000007_add_agent_run_execution_mode
-- Description: Add execution mode and plan linkage fields to agent_runs table
-- Created: 2025-11-07

-- +goose Up

ALTER TABLE agent_runs
    ADD COLUMN execution_mode ENUM('normal','plan_creation','plan_execution') NOT NULL DEFAULT 'normal' AFTER agent_type,
    ADD COLUMN plan_content TEXT NULL AFTER execution_mode,
    ADD COLUMN review_feedback_id INT NULL AFTER plan_content;

CREATE INDEX idx_agent_runs_review_feedback_id ON agent_runs(review_feedback_id);

-- +goose Down

DROP INDEX IF EXISTS idx_agent_runs_review_feedback_id ON agent_runs;

ALTER TABLE agent_runs
    DROP COLUMN IF EXISTS review_feedback_id,
    DROP COLUMN IF EXISTS plan_content,
    DROP COLUMN IF EXISTS execution_mode;


