-- Migration: 000002_add_github_issue_id (single file)
-- Description: Add github_issue_id column to issues table
-- Created: 2025-11-01

-- +goose Up

-- Add github_issue_id column to issues table
ALTER TABLE issues ADD COLUMN github_issue_id INT NULL;

-- Create index on github_issue_id for faster lookups
CREATE INDEX idx_issues_github_issue_id ON issues(github_issue_id);

-- +goose Down

-- Drop index first
DROP INDEX IF EXISTS idx_issues_github_issue_id ON issues;

-- Drop column
ALTER TABLE issues DROP COLUMN IF EXISTS github_issue_id;


