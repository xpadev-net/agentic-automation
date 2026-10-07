package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicURL_Unset(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	require.Equal(t, "", PublicURL())
}

func TestPublicURL_TrimsTrailingSlash(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://ui.example.com/")
	require.Equal(t, "https://ui.example.com", PublicURL())

	t.Setenv("PUBLIC_URL", "https://ui.example.com///")
	require.Equal(t, "https://ui.example.com", PublicURL())
}

func TestAgentRunURL_UnsetReturnsEmpty(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	require.Equal(t, "", AgentRunURL(42))
}

func TestAgentRunURL_NonPositiveIDReturnsEmpty(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://ui.example.com")
	require.Equal(t, "", AgentRunURL(0))
	require.Equal(t, "", AgentRunURL(-1))
}

func TestAgentRunURL_BuildsRunLink(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://ui.example.com/")
	require.Equal(t, "https://ui.example.com/runs/42", AgentRunURL(42))
}
