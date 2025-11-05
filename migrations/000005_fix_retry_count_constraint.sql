-- Migration: 000005_fix_retry_count_constraint
-- Description: Fix retry_count CHECK constraint to enforce max 50 attempts (0-49 range)
-- Created: 2025-11-05
-- Issue: Spec states "max 50 attempts" but constraint allowed retry_count <= 50 (51st attempt possible)
-- Fix: Change constraint to retry_count < 50 (0-49 allowed, exactly 50 attempts)

-- +goose Up

-- Drop existing constraint
ALTER TABLE agent_runs DROP CONSTRAINT chk_retry_count;

-- Add corrected constraint
ALTER TABLE agent_runs ADD CONSTRAINT chk_retry_count CHECK (retry_count < 50);

-- +goose Down

-- Revert to original constraint
ALTER TABLE agent_runs DROP CONSTRAINT chk_retry_count;
ALTER TABLE agent_runs ADD CONSTRAINT chk_retry_count CHECK (retry_count <= 50);
