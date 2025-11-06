package utils

import (
	"strings"
	"testing"
)

func TestBuildGraphSnapshot_HashAndNoTruncate(t *testing.T) {
	g := NewDependencyGraph()
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)

	jsonStr, truncated, hash := BuildGraphSnapshot(g, 1024*1024)
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(hash) == 0 {
		t.Fatalf("expected non-empty hash")
	}
	if !strings.Contains(jsonStr, "\"nodes\"") || !strings.Contains(jsonStr, "\"edges\"") {
		t.Fatalf("expected nodes and edges in json: %s", jsonStr)
	}
}

func TestBuildGraphSnapshot_Truncates(t *testing.T) {
	g := NewDependencyGraph()
	// create a larger graph
	for i := 0; i < 200; i++ {
		g.AddEdge(i, i+1)
	}

	jsonStr, truncated, hash := BuildGraphSnapshot(g, 200)
	if !truncated {
		t.Fatalf("expected truncation")
	}
	if len(hash) == 0 {
		t.Fatalf("expected non-empty hash")
	}
	if len(jsonStr) != 200 {
		t.Fatalf("expected json length 200, got %d", len(jsonStr))
	}
}
