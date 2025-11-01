-- Migration: 000002_add_github_issue_id
-- Description: Add github_issue_id column to issues table
-- Created: 2025-11-01

-- Add github_issue_id column to issues table
ALTER TABLE issues ADD COLUMN github_issue_id INT NULL;

-- Create index on github_issue_id for faster lookups
CREATE INDEX idx_issues_github_issue_id ON issues(github_issue_id);

