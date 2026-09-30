package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProgressNeverIncludesAgentContent(t *testing.T) {
	for _, raw := range []string{
		`{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"fixture-secret-in-tool-output"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"fixture-secret-in-model-text"}}`,
		`{"type":"fixture-secret-in-event-type","item":{"type":"fixture-secret-in-item-type"}}`,
		`malformed fixture-secret-in-json`,
	} {
		if strings.Contains(progressSummary(raw), "fixture-secret") {
			t.Fatalf("content reached progress: %q", raw)
		}
	}
}

func TestStreamingPreservesParserBytesWithoutPublishingSecrets(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "fixture-runtime-key")
	dir := t.TempDir()
	event, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "fixture-runtime-key"}})
	// The shell emits the credential in separate writes, and stderr in separate
	// chunks. No raw stream fragment should ever be published as progress.
	script := "#!/bin/sh\nprintf '%s' '" + string(event[:20]) + "'\nprintf '%s\\n' '" + string(event[20:]) + "'\nprintf 'fixture-' >&2\nprintf 'stderr-secret\\n' >&2\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var progress strings.Builder
	executor := NewExecutor("codex")
	executor.progressWriter = &progress
	output, err := executor.Execute(t.TempDir(), "fixture prompt")
	if err != nil {
		t.Fatal(err)
	}
	if output != string(event)+"\n" {
		t.Fatalf("parser bytes changed: %q", output)
	}
	if strings.Contains(progress.String(), "fixture-") {
		t.Fatalf("fixture secret reached progress: %q", progress.String())
	}
	if !strings.Contains(progress.String(), "agent_message") {
		t.Fatal("missing useful progress summary")
	}
}

func TestStreamFailureClosesPipesInheritedByChild(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "fixture-runtime-key")
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\nsleep 30 >&2 &\necho $! > '" + pidFile + "'\nprintf '%s\\n' '{\"type\":\"turn.started\"}'\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	}()
	executor := NewExecutor("codex")
	executor.progressWriter = failingWriter{}
	done := make(chan error, 1)
	go func() { _, err := executor.Execute(dir, "fixture prompt"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected progress failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inherited pipe blocked error cleanup")
	}
}
