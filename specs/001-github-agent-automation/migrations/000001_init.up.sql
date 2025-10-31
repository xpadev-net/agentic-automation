-- Migration: 000001_init
-- Description: Initial schema for GitHub Agent Automation
-- Created: 2025-11-01

-- Table: issues
CREATE TABLE IF NOT EXISTS issues (
    id INT AUTO_INCREMENT PRIMARY KEY,
    repo VARCHAR(255) NOT NULL,
    number INT NOT NULL,
    title VARCHAR(512) NOT NULL,
    body TEXT,
    labels JSON,
    state ENUM('open', 'closed') NOT NULL DEFAULT 'open',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    UNIQUE KEY uk_issue_repo_number (repo, number),
    INDEX idx_issue_state (state),
    INDEX idx_issue_created (created_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: pull_requests
CREATE TABLE IF NOT EXISTS pull_requests (
    id INT AUTO_INCREMENT PRIMARY KEY,
    repo VARCHAR(255) NOT NULL,
    number INT NOT NULL,
    issue_id INT,
    branch VARCHAR(255) NOT NULL,
    base_branch VARCHAR(255) NOT NULL DEFAULT 'main',
    status ENUM('open', 'closed', 'merged') NOT NULL DEFAULT 'open',
    mergeable BOOLEAN,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    UNIQUE KEY uk_pr_repo_number (repo, number),
    INDEX idx_pr_issue (issue_id),
    INDEX idx_pr_status (status),
    FOREIGN KEY fk_pr_issue (issue_id) REFERENCES issues(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: agent_runs
CREATE TABLE IF NOT EXISTS agent_runs (
    id INT AUTO_INCREMENT PRIMARY KEY,
    idempotency_key VARCHAR(191) NOT NULL,
    issue_id INT NOT NULL,
    pr_id INT,
    state ENUM('queued', 'started', 'succeeded', 'failed') NOT NULL DEFAULT 'queued',
    agent_type ENUM('claude-code', 'cursor-agents') NOT NULL DEFAULT 'claude-code',
    input JSON,
    output JSON,
    retry_count INT NOT NULL DEFAULT 0,
    error_message TEXT,
    commit_sha VARCHAR(191),
    started_at DATETIME,
    completed_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    UNIQUE KEY uk_agent_run_idempotency (idempotency_key),
    INDEX idx_agent_run_state (state),
    INDEX idx_agent_run_issue (issue_id),
    INDEX idx_agent_run_pr (pr_id),
    INDEX idx_agent_run_retry (retry_count),
    FOREIGN KEY fk_agent_run_issue (issue_id) REFERENCES issues(id) ON DELETE CASCADE,
    FOREIGN KEY fk_agent_run_pr (pr_id) REFERENCES pull_requests(id) ON DELETE SET NULL,

    CONSTRAINT chk_retry_count CHECK (retry_count <= 50)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: review_feedback
CREATE TABLE IF NOT EXISTS review_feedback (
    id INT AUTO_INCREMENT PRIMARY KEY,
    pr_id INT NOT NULL,
    source ENUM('Codex') NOT NULL DEFAULT 'Codex',
    content TEXT,
    status ENUM('requested', 'received', 'commented') NOT NULL DEFAULT 'requested',
    approval_detected BOOLEAN NOT NULL DEFAULT FALSE,
    github_comment_id BIGINT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_review_pr (pr_id),
    INDEX idx_review_approval (approval_detected),
    INDEX idx_review_status (status),
    FOREIGN KEY fk_review_pr (pr_id) REFERENCES pull_requests(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: ci_status
CREATE TABLE IF NOT EXISTS ci_status (
    id INT AUTO_INCREMENT PRIMARY KEY,
    pr_id INT NOT NULL,
    check_suite_id VARCHAR(191) NOT NULL,
    check_run_id VARCHAR(191),
    name VARCHAR(191) NOT NULL,
    status ENUM('queued', 'in_progress', 'completed') NOT NULL DEFAULT 'queued',
    conclusion ENUM('success', 'failure', 'cancelled', 'skipped', 'neutral'),
    logs TEXT,
    logs_url VARCHAR(512),
    started_at DATETIME,
    completed_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    UNIQUE KEY uk_ci_status_unique (pr_id, check_suite_id, check_run_id),
    INDEX idx_ci_status_pr_status (pr_id, status),
    INDEX idx_ci_status_conclusion (conclusion),
    FOREIGN KEY fk_ci_status_pr (pr_id) REFERENCES pull_requests(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: blocker_graph_edges
CREATE TABLE IF NOT EXISTS blocker_graph_edges (
    task_id INT NOT NULL,
    depends_on_task_id INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY pk_blocker_graph (task_id, depends_on_task_id),
    INDEX idx_blocker_depends_on (depends_on_task_id),
    FOREIGN KEY fk_blocker_task (task_id) REFERENCES issues(id) ON DELETE CASCADE,
    FOREIGN KEY fk_blocker_depends (depends_on_task_id) REFERENCES issues(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: audit_logs
CREATE TABLE IF NOT EXISTS audit_logs (
    id INT AUTO_INCREMENT PRIMARY KEY,
    event_type VARCHAR(191) NOT NULL,
    actor VARCHAR(191) NOT NULL,
    resource_type VARCHAR(191) NOT NULL,
    resource_id INT NOT NULL,
    payload JSON,
    idempotency_key VARCHAR(191),
    ip_address VARCHAR(45),
    user_agent TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_audit_event_created (event_type, created_at DESC),
    INDEX idx_audit_resource (resource_type, resource_id),
    INDEX idx_audit_actor (actor),
    INDEX idx_audit_idempotency (idempotency_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: operation_logs
CREATE TABLE IF NOT EXISTS operation_logs (
    id INT AUTO_INCREMENT PRIMARY KEY,
    run_id INT NOT NULL,
    operation_type ENUM('pr-create', 'post-comment', 'request-review', 'merge') NOT NULL,
    operation_id VARCHAR(191) NOT NULL,
    status ENUM('pending', 'succeeded', 'failed') NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE KEY uk_operation_id (operation_id),
    INDEX idx_operation_run_type (run_id, operation_type),
    FOREIGN KEY fk_operation_run (run_id) REFERENCES agent_runs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Table: branch_locks
CREATE TABLE IF NOT EXISTS branch_locks (
    branch_name VARCHAR(255) PRIMARY KEY,
    agent_run_id INT NOT NULL,
    locked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY fk_branch_lock_run (agent_run_id) REFERENCES agent_runs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
