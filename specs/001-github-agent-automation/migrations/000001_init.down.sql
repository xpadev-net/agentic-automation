-- Migration: 000001_init (DOWN)
-- Description: Rollback initial schema
-- Created: 2025-11-01

-- Drop tables in reverse order (respecting foreign key constraints)
DROP TABLE IF EXISTS branch_locks;
DROP TABLE IF EXISTS operation_logs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS blocker_graph_edges;
DROP TABLE IF EXISTS ci_status;
DROP TABLE IF EXISTS review_feedback;
DROP TABLE IF EXISTS agent_runs;
DROP TABLE IF EXISTS pull_requests;
DROP TABLE IF EXISTS issues;
