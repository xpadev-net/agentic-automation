-- Migration: 000006_add_review_feedback_plan_fields
-- Description: Add plan creation/execution tracking fields to review_feedback table
-- Created: 2025-11-07

-- +goose Up

ALTER TABLE review_feedback
    ADD COLUMN plan_creation_status ENUM('pending','creating','created','rejected','executed') NOT NULL DEFAULT 'pending' AFTER github_comment_id,
    ADD COLUMN plan_content TEXT NULL AFTER plan_creation_status,
    ADD COLUMN plan_agent_run_id INT NULL AFTER plan_content,
    ADD COLUMN execution_agent_run_id INT NULL AFTER plan_agent_run_id;

CREATE INDEX idx_review_feedback_plan_agent_run_id ON review_feedback(plan_agent_run_id);
CREATE INDEX idx_review_feedback_execution_agent_run_id ON review_feedback(execution_agent_run_id);

-- +goose Down

DROP INDEX IF EXISTS idx_review_feedback_execution_agent_run_id ON review_feedback;
DROP INDEX IF EXISTS idx_review_feedback_plan_agent_run_id ON review_feedback;

ALTER TABLE review_feedback
    DROP COLUMN IF EXISTS execution_agent_run_id,
    DROP COLUMN IF EXISTS plan_agent_run_id,
    DROP COLUMN IF EXISTS plan_content,
    DROP COLUMN IF EXISTS plan_creation_status;


