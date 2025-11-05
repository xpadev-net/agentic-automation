package services_test

import (
	"agentic-automation/internal/services"
	"testing"

	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/require"
)

func newCheckRun(status, conclusion string) *github.CheckRun {
	s := status
	var c *string
	if conclusion != "" {
		cc := conclusion
		c = &cc
	}
	return &github.CheckRun{Status: &s, Conclusion: c}
}

func TestMapConclusion(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := callMap("completed", "success")
		require.Equal(t, "success", r)
	})
	t.Run("neutral as success", func(t *testing.T) {
		r := callMap("completed", "neutral")
		require.Equal(t, "success", r)
	})
	t.Run("skipped as success", func(t *testing.T) {
		r := callMap("completed", "skipped")
		require.Equal(t, "success", r)
	})
	t.Run("queued is pending", func(t *testing.T) {
		r := callMap("queued", "")
		require.Equal(t, "pending", r)
	})
	t.Run("in_progress is pending", func(t *testing.T) {
		r := callMap("in_progress", "")
		require.Equal(t, "pending", r)
	})
	t.Run("failure is failed", func(t *testing.T) {
		r := callMap("completed", "failure")
		require.Equal(t, "failed", r)
	})
	t.Run("cancelled is failed", func(t *testing.T) {
		r := callMap("completed", "cancelled")
		require.Equal(t, "failed", r)
	})
}

// call private function via same-package test shim
func callMap(status, conclusion string) string {
	return services.MapConclusion(status, conclusion)
}

func TestAggregateFromRuns_Priority(t *testing.T) {
	// failed present -> failed
	{
		runs := []*github.CheckRun{newCheckRun("completed", "success"), newCheckRun("completed", "failure")}
		agg := services.AggregateFromRuns(runs)
		require.Equal(t, "failed", agg.Aggregated)
	}
	// pending present (no failed) -> pending
	{
		runs := []*github.CheckRun{newCheckRun("queued", ""), newCheckRun("completed", "neutral")}
		agg := services.AggregateFromRuns(runs)
		require.Equal(t, "pending", agg.Aggregated)
	}
	// success-only -> success
	{
		runs := []*github.CheckRun{newCheckRun("completed", "success"), newCheckRun("completed", "skipped")}
		agg := services.AggregateFromRuns(runs)
		require.Equal(t, "success", agg.Aggregated)
	}
	// zero runs -> pending
	{
		var runs []*github.CheckRun
		agg := services.AggregateFromRuns(runs)
		require.Equal(t, "pending", agg.Aggregated)
	}
}
