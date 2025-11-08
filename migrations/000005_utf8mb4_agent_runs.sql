-- Migration: 000005_utf8mb4_agent_runs
-- Description: Ensure utf8mb4 charset/collation for agent_runs textual columns
-- Created: 2025-11-08

-- +goose Up

-- Ensure utf8mb4 charset/collation for agent_runs textual columns
ALTER TABLE `agent_runs`
  MODIFY `error_message` TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL,
  MODIFY `agent_type` ENUM('claude-code','cursor-agent') CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  MODIFY `state` ENUM('queued','started','succeeded','failed') CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  MODIFY `commit_sha` VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL,
  MODIFY `s3_session_key` VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL;

-- JSON columns are internally UTF-8; ensure table default is utf8mb4 as well
ALTER TABLE `agent_runs` CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- +goose Down

-- Revert table default charset/collation (assuming it was utf8mb4_unicode_ci before)
ALTER TABLE `agent_runs` CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- Revert column modifications (restore to utf8mb4_unicode_ci)
ALTER TABLE `agent_runs`
  MODIFY `error_message` TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL,
  MODIFY `agent_type` ENUM('claude-code','cursor-agent') CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  MODIFY `state` ENUM('queued','started','succeeded','failed') CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  MODIFY `commit_sha` VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL,
  MODIFY `s3_session_key` VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL;


