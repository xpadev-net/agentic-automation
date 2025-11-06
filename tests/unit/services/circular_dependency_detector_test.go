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
