package services

import (
	"fmt"
	"strings"

	"agentic-automation/internal/models"
	"agentic-automation/internal/utils"
	"go.uber.org/zap"
)

// CircularDependencyError represents a detected cycle in the dependency graph.
// Cycle is a closed path where the first and last elements are equal.
type CircularDependencyError struct {
	Cycle []int
	Code  utils.ErrorCode
}

func (e *CircularDependencyError) Error() string {
	if len(e.Cycle) == 0 {
		return "circular dependency detected"
	}
	parts := make([]string, 0, len(e.Cycle))
	for _, id := range e.Cycle {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return "circular dependency detected: " + strings.Join(parts, " -> ")
}

// GetErrorCode returns the error code for this error
func (e *CircularDependencyError) GetErrorCode() utils.ErrorCode {
	if e.Code != "" {
		return e.Code
	}
	return utils.ERR_DEPENDENCY_CIRCULAR
}

// CircularDependencyDetector provides cycle detection over a dependency graph.
type CircularDependencyDetector struct {
	logger *zap.Logger
}

// NewCircularDependencyDetector constructs a new detector with the provided logger.
func NewCircularDependencyDetector(logger *zap.Logger) *CircularDependencyDetector {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &CircularDependencyDetector{logger: logger}
}

// DetectCycleFromGraph detects a single cycle from the given graph.
// Returns (cycle, *CircularDependencyError) where cycle is a closed path if found.
func (d *CircularDependencyDetector) DetectCycleFromGraph(g *utils.DependencyGraph) ([]int, *CircularDependencyError) {
	if g == nil {
		return nil, nil
	}
	if cycle, ok := g.DetectCycle(); ok {
		err := &CircularDependencyError{
			Cycle: cycle,
			Code:  utils.ERR_DEPENDENCY_CIRCULAR,
		}
		d.logger.Info("circular dependency detected", zap.Ints("cycle", cycle))
		return cycle, err
	}
	return nil, nil
}

// DetectCycleFromEdges constructs a graph from edges and detects a single cycle.
func (d *CircularDependencyDetector) DetectCycleFromEdges(edges []models.BlockerGraphEdge) ([]int, *CircularDependencyError) {
	g := utils.FromEdges(edges)
	return d.DetectCycleFromGraph(g)
}

// FindAllCyclesFromGraph finds up to 'limit' cycles in the graph.
func (d *CircularDependencyDetector) FindAllCyclesFromGraph(g *utils.DependencyGraph, limit int) [][]int {
	if g == nil || limit <= 0 {
		return [][]int{}
	}
	cycles := g.FindAllCycles(limit)
	if len(cycles) > 0 {
		d.logger.Debug("cycles found", zap.Int("count", len(cycles)))
	}
	return cycles
}

// ValidateAcyclic returns nil if the graph has no cycle, otherwise *CircularDependencyError.
func (d *CircularDependencyDetector) ValidateAcyclic(g *utils.DependencyGraph) error {
	if g == nil {
		return nil
	}
	if cycle, ok := g.DetectCycle(); ok {
		err := &CircularDependencyError{
			Cycle: cycle,
			Code:  utils.ERR_DEPENDENCY_CIRCULAR,
		}
		d.logger.Info("circular dependency detected", zap.Ints("cycle", cycle))
		return err
	}
	return nil
}
