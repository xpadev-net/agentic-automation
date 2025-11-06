package services

import (
	"testing"

	"agentic-automation/internal/utils"
)

func TestTruncateIDsForInfo(t *testing.T) {
	ids := []int{1, 2, 3, 4, 5}
	shown, truncated := truncateIDsForInfo(ids, 3)
	if !truncated {
		t.Fatalf("expected truncated=true")
	}
	if len(shown) != 3 || shown[0] != 1 || shown[2] != 3 {
		t.Fatalf("unexpected shown slice: %#v", shown)
	}

	shown2, truncated2 := truncateIDsForInfo(ids, 10)
	if truncated2 {
		t.Fatalf("expected truncated=false")
	}
	if len(shown2) != len(ids) {
		t.Fatalf("expected full slice, got %d", len(shown2))
	}
}

func TestGraphHashForServicesUsage(t *testing.T) {
	g := utils.NewDependencyGraph()
	g.AddEdge(10, 20)
	jsonStr, truncated, hash := utils.BuildGraphSnapshot(g, 1024)
	if truncated {
		t.Fatalf("unexpected truncation for small graph")
	}
	if len(hash) == 0 || len(jsonStr) == 0 {
		t.Fatalf("expected non-empty hash and json")
	}
}
