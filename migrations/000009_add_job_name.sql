-- Migration: 000009_add_job_name
-- Description: Add job_name column to agent_runs table to store the actual Kubernetes Job name for cleanup
-- Created: 2025-01-XX

-- +goose Up

-- Add job_name column after execution_mode
ALTER TABLE agent_runs
    ADD COLUMN job_name VARCHAR(255) NULL AFTER execution_mode;

-- +goose Down

ALTER TABLE agent_runs
    DROP COLUMN IF EXISTS job_name;

