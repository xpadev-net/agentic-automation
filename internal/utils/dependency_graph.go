package utils

import (
	"errors"
	"sort"

	"agentic-automation/internal/models"
)

// ErrCycleDetected is returned when a topological order cannot be produced due to cycles.
var ErrCycleDetected = errors.New("dependency cycle detected")

// DependencyGraph is an in-memory directed graph for task dependencies.
// Edge direction follows: dependsOn -> task (prerequisite -> dependent).
// It is not concurrency-safe; callers should provide synchronization if needed.
type DependencyGraph struct {
	nodes    map[int]struct{}
	outEdges map[int]map[int]struct{} // from -> {to}
	inEdges  map[int]map[int]struct{} // to -> {from}
}

// NewDependencyGraph creates an empty dependency graph.
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		nodes:    make(map[int]struct{}),
		outEdges: make(map[int]map[int]struct{}),
		inEdges:  make(map[int]map[int]struct{}),
	}
}

// FromEdges constructs a dependency graph from BlockerGraphEdge slice.
// Direction: edge.DependsOnTaskID -> edge.TaskID.
func FromEdges(edges []models.BlockerGraphEdge) *DependencyGraph {
	g := NewDependencyGraph()
	for _, e := range edges {
		// dependsOn -> task
		g.AddEdge(e.DependsOnTaskID, e.TaskID)
	}
	return g
}

// AddNode ensures a node exists in the graph.
func (g *DependencyGraph) AddNode(id int) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
}

// AddEdge adds a directed edge from -> to. Nodes are auto-created if missing.
// Self-loops are allowed at insertion time; algorithms detect/report cycles as needed.
func (g *DependencyGraph) AddEdge(from, to int) {
	g.AddNode(from)
	g.AddNode(to)

	if g.outEdges[from] == nil {
		g.outEdges[from] = make(map[int]struct{})
	}
	if g.inEdges[to] == nil {
		g.inEdges[to] = make(map[int]struct{})
	}
	g.outEdges[from][to] = struct{}{}
	g.inEdges[to][from] = struct{}{}
}

// RemoveEdge removes a directed edge from -> to. No-op if absent.
func (g *DependencyGraph) RemoveEdge(from, to int) {
	if tos, ok := g.outEdges[from]; ok {
		delete(tos, to)
		if len(tos) == 0 {
			delete(g.outEdges, from)
		}
	}
	if froms, ok := g.inEdges[to]; ok {
		delete(froms, from)
		if len(froms) == 0 {
			delete(g.inEdges, to)
		}
	}
}

// HasNode returns true if the node exists.
func (g *DependencyGraph) HasNode(id int) bool {
	_, ok := g.nodes[id]
	return ok
}

// Nodes returns all node IDs in ascending order.
func (g *DependencyGraph) Nodes() []int {
	ids := make([]int, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Edges returns all edges as [2]int pairs [from, to] sorted lexicographically.
func (g *DependencyGraph) Edges() [][2]int {
	pairs := make([][2]int, 0)
	for from, tos := range g.outEdges {
		for to := range tos {
			pairs = append(pairs, [2]int{from, to})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] == pairs[j][0] {
			return pairs[i][1] < pairs[j][1]
		}
		return pairs[i][0] < pairs[j][0]
	})
	return pairs
}

// DependenciesOf returns direct prerequisites of id (incoming neighbors) in ascending order.
func (g *DependencyGraph) DependenciesOf(id int) []int {
	srcs := g.inEdges[id]
	if len(srcs) == 0 {
		return []int{}
	}
	res := make([]int, 0, len(srcs))
	for u := range srcs {
		res = append(res, u)
	}
	sort.Ints(res)
	return res
}

// DependentsOf returns direct dependents of id (outgoing neighbors) in ascending order.
func (g *DependencyGraph) DependentsOf(id int) []int {
	tos := g.outEdges[id]
	if len(tos) == 0 {
		return []int{}
	}
	res := make([]int, 0, len(tos))
	for v := range tos {
		res = append(res, v)
	}
	sort.Ints(res)
	return res
}

// InDegree returns the number of direct prerequisites of id.
func (g *DependencyGraph) InDegree(id int) int {
	return len(g.inEdges[id])
}

// OutDegree returns the number of direct dependents of id.
func (g *DependencyGraph) OutDegree(id int) int {
	return len(g.outEdges[id])
}

// TopologicalSort performs Kahn's algorithm and returns nodes in dependency order.
// On cycles, returns ErrCycleDetected.
func (g *DependencyGraph) TopologicalSort() ([]int, error) {
	// Compute indegrees
	indeg := make(map[int]int, len(g.nodes))
	for id := range g.nodes {
		indeg[id] = len(g.inEdges[id])
	}

	// Initialize queue with zero-indegree nodes (ascending for determinism)
	zero := make([]int, 0)
	for id, d := range indeg {
		if d == 0 {
			zero = append(zero, id)
		}
	}
	sort.Ints(zero)

	order := make([]int, 0, len(g.nodes))
	for len(zero) > 0 {
		// pop smallest
		u := zero[0]
		zero = zero[1:]
		order = append(order, u)

		// For each neighbor, decrease indegree
		neighbors := make([]int, 0, len(g.outEdges[u]))
		for v := range g.outEdges[u] {
			neighbors = append(neighbors, v)
		}
		sort.Ints(neighbors)
		for _, v := range neighbors {
			indeg[v]--
			if indeg[v] == 0 {
				// insert keeping ascending order
				idx := sort.SearchInts(zero, v)
				zero = append(zero, 0)
				copy(zero[idx+1:], zero[idx:])
				zero[idx] = v
			}
		}
	}

	if len(order) != len(g.nodes) {
		return nil, ErrCycleDetected
	}
	return order, nil
}

// DetectCycle returns one cycle path if exists (first/last element equal for closure).
// If no cycle exists, returns (nil, false).
func (g *DependencyGraph) DetectCycle() ([]int, bool) {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[int]int, len(g.nodes))
	parent := make(map[int]int, len(g.nodes))

	// deliver neighbors in ascending order for determinism
	getNeighbors := func(u int) []int {
		tos := g.outEdges[u]
		if len(tos) == 0 {
			return []int{}
		}
		res := make([]int, 0, len(tos))
		for v := range tos {
			res = append(res, v)
		}
		sort.Ints(res)
		return res
	}

	var cycle []int

	var dfs func(u int) bool
	dfs = func(u int) bool {
		state[u] = visiting
		for _, v := range getNeighbors(u) {
			if state[v] == unvisited {
				parent[v] = u
				if dfs(v) {
					return true
				}
			} else if state[v] == visiting { // back-edge
				// reconstruct cycle u -> ... -> v -> u
				path := []int{v}
				cur := u
				for cur != v {
					path = append(path, cur)
					cur = parent[cur]
				}
				path = append(path, v)
				// reverse to start at v and end at v (closed cycle)
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				cycle = path
				return true
			}
		}
		state[u] = visited
		return false
	}

	ids := g.Nodes()
	for _, id := range ids {
		if state[id] == unvisited {
			parent[id] = id
			if dfs(id) {
				return cycle, true
			}
		}
	}
	return nil, false
}

// FindAllCycles finds up to 'limit' cycles using repeated DFS with edge skipping.
// If limit <= 0, returns empty slice.
func (g *DependencyGraph) FindAllCycles(limit int) [][]int {
	if limit <= 0 {
		return [][]int{}
	}
	cycles := make([][]int, 0, limit)

	// naive approach: remove one edge from found cycle and continue searching
	// copy of edges for mutation
	snapshot := NewDependencyGraph()
	for id := range g.nodes {
		snapshot.AddNode(id)
	}
	for from, tos := range g.outEdges {
		for to := range tos {
			snapshot.AddEdge(from, to)
		}
	}

	for len(cycles) < limit {
		if c, ok := snapshot.DetectCycle(); ok {
			cycles = append(cycles, c)
			// remove one edge of the cycle to find another
			if len(c) >= 2 {
				snapshot.RemoveEdge(c[len(c)-2], c[len(c)-1])
			} else {
				break
			}
		} else {
			break
		}
	}
	return cycles
}

// IsBlocked returns true if 'id' has at least one prerequisite not present in 'closed'.
func (g *DependencyGraph) IsBlocked(id int, closed map[int]struct{}) bool {
	for dep := range g.inEdges[id] {
		if _, ok := closed[dep]; !ok {
			return true
		}
	}
	return false
}

// UnblockedGiven returns all nodes whose prerequisites are all contained in 'closed'.
// Nodes are returned in ascending order.
func (g *DependencyGraph) UnblockedGiven(closed map[int]struct{}) []int {
	result := make([]int, 0)
	for id := range g.nodes {
		if !g.IsBlocked(id, closed) {
			result = append(result, id)
		}
	}
	sort.Ints(result)
	return result
}
