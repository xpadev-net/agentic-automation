-- Migration: 000003_add_s3_session_fields (DOWN)
-- Description: Rollback S3 session fields addition
-- Created: 2025-11-01

-- Drop session_saved_at column first (後で追加したカラムから削除)
ALTER TABLE agent_runs DROP COLUMN IF EXISTS session_saved_at;

-- Drop s3_session_key column
ALTER TABLE agent_runs DROP COLUMN IF EXISTS s3_session_key;

