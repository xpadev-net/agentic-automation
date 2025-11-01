package agent

// NewExecutor creates a new agent executor.
// Implementation will be added in T033.
func NewExecutor(agentType string) *Executor {
	return nil // TODO: implement in T033
}

// Executor executes AI agents (claude-code or cursor-agents).
// Implementation will be added in T033.
type Executor struct {
	agentType string
}
