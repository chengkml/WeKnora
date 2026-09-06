package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// GraphService exposes the Neo4j knowledge-graph operations (AddGraph,
// DelGraph, SearchNode) behind the service boundary so they can be reached
// through HTTP (and in turn by the MCP gateway). When Neo4j is disabled the
// underlying repository is a no-op driver-less stub, so these calls degrade
// gracefully.
type GraphService struct {
	graphRepo interfaces.RetrieveGraphRepository
}

// NewGraphService creates a GraphService backed by the given graph repository.
func NewGraphService(graphRepo interfaces.RetrieveGraphRepository) *GraphService {
	return &GraphService{graphRepo: graphRepo}
}

// AddGraph writes nodes and relationships for a knowledge base (and
// optionally a single knowledge/file) into the Neo4j graph.
func (s *GraphService) AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error {
	if s.graphRepo == nil {
		return errors.New("graph repository is not initialized (Neo4j disabled)")
	}
	if namespace.KnowledgeBase == "" {
		return errors.New("knowledge_base is required")
	}
	if len(graphs) == 0 {
		return errors.New("graphs payload is empty")
	}
	return s.graphRepo.AddGraph(ctx, namespace, graphs)
}

// DelGraph removes the graph data of the given namespaces. Pass one
// namespace with only KnowledgeBase set to wipe the whole KB graph; add
// Knowledge to scope the deletion to a single file.
func (s *GraphService) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	if s.graphRepo == nil {
		return errors.New("graph repository is not initialized (Neo4j disabled)")
	}
	if len(namespaces) == 0 {
		return errors.New("namespaces payload is empty")
	}
	for _, ns := range namespaces {
		if ns.KnowledgeBase == "" {
			return errors.New("each namespace requires knowledge_base")
		}
	}
	return s.graphRepo.DelGraph(ctx, namespaces)
}

// SearchNode finds graph nodes whose name contains any of the given query
// terms and returns the induced subgraph (nodes + relationships).
func (s *GraphService) SearchNode(ctx context.Context, namespace types.NameSpace, query string) (*types.GraphData, error) {
	if s.graphRepo == nil {
		return nil, errors.New("graph repository is not initialized (Neo4j disabled)")
	}
	if namespace.KnowledgeBase == "" {
		return nil, errors.New("knowledge_base is required")
	}
	nodes := splitGraphQuery(query)
	if len(nodes) == 0 {
		return nil, errors.New("query must contain at least one non-empty term")
	}
	return s.graphRepo.SearchNode(ctx, namespace, nodes)
}

// splitGraphQuery splits a free-text query into the individual node-name
// fragments used by SearchNode's CONTAINS matcher. Commas, spaces, and
// semicolons are treated as separators; empty fragments are dropped.
func splitGraphQuery(query string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range query {
		switch r {
		case ',', ';', ' ', '\t', '\n', '\r':
			flush()
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}
