package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// GraphSnapshot is a serializable view of a dependency graph.
// Nodes and Edges are sorted/normalized by DependencyGraph methods
// to ensure stable JSON and hash results.
type GraphSnapshot struct {
	Nodes []int    `json:"nodes"`
	Edges [][2]int `json:"edges"`
}

// BuildGraphSnapshot renders the given graph into JSON with a size limit and
// returns (jsonString, truncated, sha256HexHash).
// The hash is computed from the canonical JSON (before truncation) to provide
// a stable digest irrespective of size limits.
func BuildGraphSnapshot(g *DependencyGraph, maxBytes int) (string, bool, string) {
	if g == nil {
		return "{}", false, ""
	}

	snap := GraphSnapshot{
		Nodes: g.Nodes(),
		Edges: g.Edges(),
	}

	full, _ := json.Marshal(snap)
	// Compute hash on full content for stability
	sha := sha256.Sum256(full)
	hash := hex.EncodeToString(sha[:])

	// Enforce size limit for emitted JSON string
	if maxBytes > 0 && len(full) > maxBytes {
		return string(full[:maxBytes]), true, hash
	}
	return string(full), false, hash
}
