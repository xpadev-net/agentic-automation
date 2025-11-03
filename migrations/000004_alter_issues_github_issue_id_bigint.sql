-- Migration: 000004_alter_issues_github_issue_id_bigint
-- Description: Expand issues.github_issue_id to BIGINT UNSIGNED to avoid overflow
-- Created: 2025-11-03

-- +goose Up

ALTER TABLE issues
  MODIFY COLUMN github_issue_id BIGINT UNSIGNED NULL;

-- +goose Down

ALTER TABLE issues
  MODIFY COLUMN github_issue_id INT NULL;


