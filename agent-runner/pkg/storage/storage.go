package storage

import "fmt"

// SaveSession saves agent session data to S3.
// Implementation will be added in T052_S3.
func SaveSession(agentRunID int, agentType string) error {
	return fmt.Errorf("not implemented: T052_S3")
}

// RestoreSession restores agent session data from S3.
// Implementation will be added in T052_S3.
func RestoreSession(agentRunID int, retryCount int) error {
	return fmt.Errorf("not implemented: T052_S3")
}
