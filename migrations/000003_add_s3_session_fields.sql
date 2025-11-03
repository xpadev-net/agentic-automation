-- Migration: 000003_add_s3_session_fields (single file)
-- Description: Add S3 session tracking fields to agent_runs table
-- Created: 2025-11-01

-- +goose Up

-- Add s3_session_key column to agent_runs table
ALTER TABLE agent_runs ADD COLUMN s3_session_key VARCHAR(512) NULL;

-- Add session_saved_at column to agent_runs table
ALTER TABLE agent_runs ADD COLUMN session_saved_at DATETIME NULL;

-- +goose Down

-- Drop session_saved_at column first (後で追加したカラムから削除)
ALTER TABLE agent_runs DROP COLUMN IF EXISTS session_saved_at;

-- Drop s3_session_key column
ALTER TABLE agent_runs DROP COLUMN IF EXISTS s3_session_key;


