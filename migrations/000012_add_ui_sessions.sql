-- Migration: 000012_add_ui_sessions
-- Description: Add ui_sessions table for WebUI GitHub OAuth sessions
-- Created: 2026-10-07

-- +goose Up

-- Table: ui_sessions
-- Stores WebUI login sessions created via the GitHub OAuth flow.
-- access_token is AES-256-GCM encrypted before persistence.
CREATE TABLE IF NOT EXISTS ui_sessions (
    id VARCHAR(64) NOT NULL PRIMARY KEY COMMENT 'Opaque session token (hex)',
    github_login VARCHAR(191) NOT NULL,
    access_token TEXT NOT NULL COMMENT 'AES-256-GCM encrypted OAuth token',
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_ui_session_login (github_login),
    INDEX idx_ui_session_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down

DROP TABLE IF EXISTS ui_sessions;
