package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	var (
		issueID          int
		repo             string
		prompt           string
		previousAttempts string
		ciLogs           string
	)

	rootCmd := &cobra.Command{
		Use:   "agent-runner",
		Short: "Execute AI agent for GitHub Issue automation",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(issueID, repo, prompt, previousAttempts, ciLogs)
		},
	}

	rootCmd.Flags().IntVar(&issueID, "issue-id", 0, "GitHub Issue ID")
	rootCmd.Flags().StringVar(&repo, "repo", "", "Repository (owner/name)")
	rootCmd.Flags().StringVar(&prompt, "prompt", "", "Issue context prompt")
	rootCmd.Flags().StringVar(&previousAttempts, "previous-attempts", "", "Previous retry attempts JSON")
	rootCmd.Flags().StringVar(&ciLogs, "ci-logs", "", "CI failure logs")

	rootCmd.MarkFlagRequired("issue-id")
	rootCmd.MarkFlagRequired("repo")
	rootCmd.MarkFlagRequired("prompt")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(issueID int, repo, prompt, previousAttempts, ciLogs string) error {
	// Implementation will be added in T032-T048
	return fmt.Errorf("not implemented")
}
