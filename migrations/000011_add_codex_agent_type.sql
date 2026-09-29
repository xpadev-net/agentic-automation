-- Migration: 000011_add_codex_agent_type
-- Description: Add 'codex' to agent_runs.agent_type ENUM for OpenAI Codex CLI support
-- Created: 2026-09-29

-- +goose Up

ALTER TABLE `agent_runs`
  MODIFY `agent_type` ENUM('claude-code','cursor-agent','codex') CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'claude-code';

-- +goose Down

-- Revert to the pre-codex enum values. Fails if any rows use 'codex'.
ALTER TABLE `agent_runs`
  MODIFY `agent_type` ENUM('claude-code','cursor-agent') CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'claude-code';
