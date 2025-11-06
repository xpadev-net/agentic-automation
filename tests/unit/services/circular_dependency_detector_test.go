package services_test

import (
	"testing"

	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	"agentic-automation/internal/utils"
	"go.uber.org/zap"
)

func newDetector() *services.CircularDependencyDetector {
	return services.NewCircularDependencyDetector(zap.NewNop())
}

func TestValidateAcyclic_WithAcyclicGraph_ReturnsNil(t *testing.T) {
	g := utils.NewDependencyGraph()
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)

	det := newDetector()
	if err := det.ValidateAcyclic(g); err != nil {
		t.Fatalf("expected nil error for acyclic graph, got: %v", err)
	}
}

func TestDetectCycle_Simple2NodeCycle(t *testing.T) {
	g := utils.NewDependencyGraph()
	g.AddEdge(1, 2)
	g.AddEdge(2, 1)

	det := newDetector()
	cycle, err := det.DetectCycleFromGraph(g)
	if err == nil || cycle == nil {
		t.Fatalf("expected cycle and error, got cycle=%v err=%v", cycle, err)
	}
	if len(cycle) < 2 || cycle[0] != cycle[len(cycle)-1] {
		t.Fatalf("expected closed cycle, got: %v", cycle)
	}
}

func TestDetectCycle_TriangleCycle(t *testing.T) {
	g := utils.NewDependencyGraph()
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)
	g.AddEdge(3, 1)

	det := newDetector()
	cycle, err := det.DetectCycleFromGraph(g)
	if err == nil || cycle == nil {
		t.Fatalf("expected cycle and error, got cycle=%v err=%v", cycle, err)
	}
}

func TestDetectCycle_SelfLoop(t *testing.T) {
	g := utils.NewDependencyGraph()
	g.AddEdge(5, 5)

	det := newDetector()
	cycle, err := det.DetectCycleFromGraph(g)
	if err == nil || cycle == nil {
		t.Fatalf("expected self-loop cycle and error, got cycle=%v err=%v", cycle, err)
	}
}

func TestFindAllCycles_LimitAndClosedCycles(t *testing.T) {
	g := utils.NewDependencyGraph()
	// First cycle: 1->2->1
	g.AddEdge(1, 2)
	g.AddEdge(2, 1)
	// Second cycle: 3->4->5->3
	g.AddEdge(3, 4)
	g.AddEdge(4, 5)
	g.AddEdge(5, 3)

	det := newDetector()
	cycles := det.FindAllCyclesFromGraph(g, 5)
	if len(cycles) < 2 {
		t.Fatalf("expected at least 2 cycles, got %d", len(cycles))
	}
	for _, c := range cycles {
		if len(c) < 2 || c[0] != c[len(c)-1] {
			t.Fatalf("expected closed cycle, got: %v", c)
		}
	}
}

func TestDetectCycleFromEdges_InputEdgesPath(t *testing.T) {
	edges := []models.BlockerGraphEdge{
		{TaskID: 2, DependsOnTaskID: 1},
		{TaskID: 1, DependsOnTaskID: 2}, // creates 1<->2 cycle
	}
	det := newDetector()
	cycle, err := det.DetectCycleFromEdges(edges)
	if err == nil || cycle == nil {
		t.Fatalf("expected cycle and error, got cycle=%v err=%v", cycle, err)
	}
}

func TestFindAllCycles_LimitZeroAndOne(t *testing.T) {
	g := utils.NewDependencyGraph()
	// cycles: 1<->2 and 3->4->5->3
	g.AddEdge(1, 2)
	g.AddEdge(2, 1)
	g.AddEdge(3, 4)
	g.AddEdge(4, 5)
	g.AddEdge(5, 3)

	det := newDetector()
	if got := det.FindAllCyclesFromGraph(g, 0); len(got) != 0 {
		t.Fatalf("expected 0 cycles when limit=0, got %d", len(got))
	}
	if got := det.FindAllCyclesFromGraph(g, 1); len(got) != 1 {
		t.Fatalf("expected 1 cycle when limit=1, got %d", len(got))
	}
}

func TestDetectCycle_NilAndEmptyGraph(t *testing.T) {
	det := newDetector()
	// nil graph
	if cycle, err := det.DetectCycleFromGraph(nil); cycle != nil || err != nil {
		t.Fatalf("expected (nil,nil) for nil graph, got cycle=%v err=%v", cycle, err)
	}
	// empty graph
	g := utils.NewDependencyGraph()
	if cycle, err := det.DetectCycleFromGraph(g); cycle != nil || err != nil {
		t.Fatalf("expected no cycle for empty graph, got cycle=%v err=%v", cycle, err)
	}
}

func TestDetectCycle_Determinism_StableCycleOrder(t *testing.T) {
	g := utils.NewDependencyGraph()
	// create a cycle with multiple outgoing edges to test determinism
	// 1->2->3->1 and extra edges 1->4, 2->5 that should not affect cycle order
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)
	g.AddEdge(3, 1)
	g.AddEdge(1, 4)
	g.AddEdge(2, 5)

	det := newDetector()
	var first []int
	for i := 0; i < 5; i++ {
		cycle, err := det.DetectCycleFromGraph(g)
		if err == nil || cycle == nil {
			t.Fatalf("expected cycle and error, got cycle=%v err=%v", cycle, err)
		}
		if len(cycle) < 2 || cycle[0] != cycle[len(cycle)-1] {
			t.Fatalf("expected closed cycle, got: %v", cycle)
		}
		if i == 0 {
			first = append([]int(nil), cycle...)
		} else {
			if len(first) != len(cycle) {
				t.Fatalf("cycle length changed across runs: first=%v, now=%v", first, cycle)
			}
			for j := range cycle {
				if first[j] != cycle[j] {
					t.Fatalf("cycle order not deterministic: first=%v, now=%v", first, cycle)
				}
			}
		}
	}
}

func TestValidateAcyclic_ErrorContainsCycle(t *testing.T) {
	g := utils.NewDependencyGraph()
	g.AddEdge(7, 8)
	g.AddEdge(8, 7)

	det := newDetector()
	err := det.ValidateAcyclic(g)
	if err == nil {
		t.Fatalf("expected error for cyclic graph, got nil")
	}
	// type assertion and message content
	if _, ok := err.(*services.CircularDependencyError); !ok {
		t.Fatalf("expected *CircularDependencyError, got %T", err)
	}
	if msg := err.Error(); msg == "" || msg == "circular dependency detected" {
		t.Fatalf("expected error message to contain cycle details, got: %q", msg)
	}
}
