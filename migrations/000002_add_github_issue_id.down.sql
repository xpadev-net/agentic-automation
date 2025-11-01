-- Migration: 000002_add_github_issue_id (DOWN)
-- Description: Rollback github_issue_id column addition
-- Created: 2025-11-01

-- Drop index first
DROP INDEX IF EXISTS idx_issues_github_issue_id ON issues;

-- Drop column
ALTER TABLE issues DROP COLUMN IF EXISTS github_issue_id;

