-- Migration: 000003_add_s3_session_fields
-- Description: Add S3 session tracking fields to agent_runs table
-- Created: 2025-11-01

-- Add s3_session_key column to agent_runs table
ALTER TABLE agent_runs ADD COLUMN s3_session_key VARCHAR(512) NULL;

-- Add session_saved_at column to agent_runs table
ALTER TABLE agent_runs ADD COLUMN session_saved_at DATETIME NULL;

