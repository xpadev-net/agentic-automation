-- Migration: 000010_alter_review_feedback_text_to_longtext
-- Description: Change review_feedback.content and plan_content from TEXT to LONGTEXT
-- Created: 2025-11-23

-- +goose Up

ALTER TABLE review_feedback
  MODIFY COLUMN content LONGTEXT NULL;

ALTER TABLE review_feedback
  MODIFY COLUMN plan_content LONGTEXT NULL;

-- +goose Down

ALTER TABLE review_feedback
  MODIFY COLUMN content TEXT NULL;

ALTER TABLE review_feedback
  MODIFY COLUMN plan_content TEXT NULL;
