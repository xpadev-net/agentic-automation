-- Migration: 000008_add_plan_agent_run_id
-- Description: Add plan_agent_run_id column to agent_runs table to track relationship between plan creation and execution runs
-- Created: 2025-01-XX

-- +goose Up

-- Add plan_agent_run_id column after review_feedback_id
ALTER TABLE agent_runs
    ADD COLUMN plan_agent_run_id INT NULL AFTER review_feedback_id;

CREATE INDEX idx_agent_runs_plan_agent_run_id ON agent_runs(plan_agent_run_id);

-- +goose Down

DROP INDEX IF EXISTS idx_agent_runs_plan_agent_run_id ON agent_runs;

ALTER TABLE agent_runs
    DROP COLUMN IF EXISTS plan_agent_run_id;
