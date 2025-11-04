package handlers

// TODO (T094): Implement check_suite webhook handler
// When implementing CI failure detection and retry trigger, add NotifyRetryProgress call here:
//
// 1. After CI failure is detected, get AgentRun
// 2. Check retry_count < services.MaxRetryAttempts
// 3. Generate CI log summary (use CIFailureResult.Summary)
// 4. Get Issue and PR information
// 5. Call NotifyRetryProgress with:
//    - owner, repo: from Issue.Repo
//    - issueNumber: issue.Number
//    - prNumber: from PR (if available)
//    - retryCount: agentRun.RetryCount
//    - maxRetries: services.MaxRetryAttempts
//    - errorReason: CI log summary (truncated to 100 chars)
//    - idempotencyKey: agentRun.IdempotencyKey
//
// See internal/webhooks/handlers/agent_report.go for reference implementation.
