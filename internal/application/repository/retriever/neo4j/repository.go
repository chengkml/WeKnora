package neo4j

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver     neo4j.Driver
	nodePrefix string
}

// NewNeo4jRepository creates a new Neo4j repository
func NewNeo4jRepository(driver neo4j.Driver) interfaces.RetrieveGraphRepository {
	return &Neo4jRepository{driver: driver, nodePrefix: "ENTITY"}
}

// _remove_hyphen removes hyphens from a string
func _remove_hyphen(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// Labels returns the labels for a namespace
func (n *Neo4jRepository) Labels(namespace types.NameSpace) []string {
	res := make([]string, 0)
	for _, label := range namespace.Labels() {
		res = append(res, n.nodePrefix+_remove_hyphen(label))
	}
	return res
}

// Label returns the label for a namespace
func (n *Neo4jRepository) Label(namespace types.NameSpace) string {
	labels := n.Labels(namespace)
	return strings.Join(labels, ":")
}

// AddGraph adds a graph to the Neo4j repository
func (n *Neo4jRepository) AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	for _, graph := range graphs {
		if err := n.addGraph(ctx, namespace, graph); err != nil {
			return err
		}
	}
	return nil
}

// addGraph adds a graph to the Neo4j repository
func (n *Neo4jRepository) addGraph(ctx context.Context, namespace types.NameSpace, graph *types.GraphData) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		// Node import query
		node_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, row.props, {}) YIELD node
			SET node.chunks = apoc.coll.union(node.chunks, row.chunks)
			SET node.page_id = coalesce(row.page_id, node.page_id)
			SET node.page_slug = coalesce(row.page_slug, node.page_slug)
			RETURN distinct 'done' AS result
		`
		nodeData := []map[string]interface{}{}
		for _, node := range graph.Node {
			props := map[string][]string{"attributes": node.Attributes}
			// Pass nil (not "") when absent so the coalesce() SET above keeps
			// any existing page_id instead of overwriting it with an empty string.
			var pageID, pageSlug interface{}
			if node.PageID != "" {
				props["page_id"] = []string{node.PageID}
				pageID = node.PageID
			}
			if node.PageSlug != "" {
				props["page_slug"] = []string{node.PageSlug}
				pageSlug = node.PageSlug
			}
			nodeData = append(nodeData, map[string]interface{}{
				"name":         node.Name,
				"knowledge_id": namespace.Knowledge,
				"props":        props,
				"chunks":       node.Chunks,
				"labels":       n.Labels(namespace),
				"page_id":      pageID,
				"page_slug":    pageSlug,
			})
		}
		if _, err := tx.Run(ctx, node_import_query, map[string]interface{}{"data": nodeData}); err != nil {
			return nil, fmt.Errorf("failed to create nodes: %v", err)
		}

		// Relationship import query
		rel_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.source_labels, {name: row.source, kg: row.knowledge_id}, {}, {}) YIELD node as source
			CALL apoc.merge.node(row.target_labels, {name: row.target, kg: row.knowledge_id}, {}, {}) YIELD node as target
			CALL apoc.merge.relationship(source, row.type, {}, row.attributes, target) YIELD rel
			RETURN distinct 'done'
		`
		relData := []map[string]interface{}{}
		for _, rel := range graph.Relation {
			relData = append(relData, map[string]interface{}{
				"source":        rel.Node1,
				"target":        rel.Node2,
				"knowledge_id":  namespace.Knowledge,
				"type":          rel.Type,
				"source_labels": n.Labels(namespace),
				"target_labels": n.Labels(namespace),
				"attributes":    rel.Properties, // 边属性（rule_slug/note/predicate…），nil=不设置
			})
		}
		if _, err := tx.Run(ctx, rel_import_query, map[string]interface{}{"data": relData}); err != nil {
			return nil, fmt.Errorf("failed to create relationships: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		logger.Errorf(ctx, "failed to add graph: %v", err)
		return err
	}
	return nil
}

// DelGraph deletes a graph from the Neo4j repository
func (n *Neo4jRepository) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	result, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		totalDeleted := 0
		for _, namespace := range namespaces {
			labelExpr := n.Label(namespace) // ENTITY<kb_id>:ENTITY<kg>（kg 空时仅 ENTITY<kb_id>）

			// 单事务 DETACH DELETE：原子、确定性，错误直接冒泡到 tx.Run。
			// 旧实现用 apoc.periodic.iterate(parallel:true) 分两段删 rel/nodes，
			// 高负载下内部 batch 静默丢失（DELETE r 锁冲突/重试耗尽）→
			// 部分文档子图删不干净、旧边残留，且外层 CALL 不报错（2026-09-20 实测
			// 两库各剩 ~500 条 v2 旧类型边：制定于/关联：被作用…挂在合并后的新节点上）。
			var (
				query  string
				params map[string]interface{}
			)
			if namespace.Knowledge != "" {
				query = `MATCH (n:` + labelExpr + ` {kg: $knowledge_id}) DETACH DELETE n RETURN count(n) AS deleted`
				params = map[string]interface{}{"knowledge_id": namespace.Knowledge}
			} else {
				// 删整个 KB 图（knowledge_id 未传时）：不能按 kg='' 过滤（会匹配不到）
				query = `MATCH (n:` + labelExpr + `) DETACH DELETE n RETURN count(n) AS deleted`
				params = map[string]interface{}{}
			}
			res, err := tx.Run(ctx, query, params)
			if err != nil {
				return nil, fmt.Errorf("failed to delete graph: %v", err)
			}
			// Next 读出 count（DETACH DELETE RETURN count(n)），再 Consume 排空流，
			// 确保事务内查询完整执行（错误会通过 res.Err() 冒泡，不再静默丢批）
			for res.Next(ctx) {
				if v, ok := res.Record().Values[0].(int64); ok {
					totalDeleted += int(v)
				}
			}
			if err := res.Err(); err != nil {
				return nil, fmt.Errorf("failed to consume delete result: %v", err)
			}
		}
		return totalDeleted, nil
	})
	if err != nil {
		return err
	}
	logger.Infof(ctx, "delete graph result: %v", result)
	return nil
}

// SearchNode searches for nodes in the Neo4j repository
func (n *Neo4jRepository) SearchNode(
	ctx context.Context,
	namespace types.NameSpace,
	nodes []string,
) (*types.GraphData, error) {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		labelExpr := n.Label(namespace)
		query := `
			MATCH (n:` + labelExpr + `)-[r]-(m:` + labelExpr + `)
			WHERE ANY(nodeText IN $nodes WHERE n.name CONTAINS nodeText)
			RETURN n, r, m
		`
		params := map[string]interface{}{"nodes": nodes}
		result, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, fmt.Errorf("failed to run query: %v", err)
		}

		graphData := &types.GraphData{}
		nodeSeen := make(map[string]bool)
		for result.Next(ctx) {
			record := result.Record()
			node, _ := record.Get("n")
			rel, _ := record.Get("r")
			targetNode, _ := record.Get("m")

			nodeData := node.(neo4j.Node)
			targetNodeData := targetNode.(neo4j.Node)

			// Convert node to types.Node
			for _, n := range []neo4j.Node{nodeData, targetNodeData} {
				// Nodes created as bare relation endpoints (graph_add without
				// node attributes) may lack chunks/attributes props entirely —
				// guard the type assertions so search doesn't 500 on them.
				nameStr, _ := n.Props["name"].(string)
				if nameStr == "" || nodeSeen[nameStr] {
					continue
				}
				nodeSeen[nameStr] = true
				graphData.Node = append(graphData.Node, &types.GraphNode{
					Name:       nameStr,
					Chunks:     propToStringSlice(n.Props["chunks"]),
					Attributes: propToStringSlice(n.Props["attributes"]),
					PageID:     propToString(n.Props["page_id"]),
					PageSlug:   propToString(n.Props["page_slug"]),
				})
			}

			// Convert relationship to types.Relation
			relData := rel.(neo4j.Relationship)
			relProps := make(map[string]string, len(relData.Props))
			for k, v := range relData.Props {
				if s, ok := v.(string); ok {
					relProps[k] = s
				}
			}
			graphData.Relation = append(graphData.Relation, &types.GraphRelation{
				Node1:      nodeData.Props["name"].(string),
				Node2:      targetNodeData.Props["name"].(string),
				Type:       relData.Type,
				Properties: relProps,
			})
		}
		return graphData, nil
	})
	if err != nil {
		logger.Errorf(ctx, "search node failed: %v", err)
		return nil, err
	}
	return result.(*types.GraphData), nil
}

// propToStringSlice safely converts a Neo4j property (nil, []interface{},
// []string, or scalar) into []string for GraphNode.Chunks/Attributes.
func propToStringSlice(prop interface{}) []string {
	switch v := prop.(type) {
	case nil:
		return nil
	case []interface{}:
		return listI2listS(v)
	case []string:
		return v
	default:
		return nil
	}
}

func listI2listS(list []any) []string {
	result := make([]string, len(list))
	for i, v := range list {
		result[i] = fmt.Sprintf("%v", v)
	}
	return result
}

// propToString reads a single-valued Neo4j property (string, or a 1-element
// list) into a string. Used for page_id / page_slug, which are written both
// as scalars and as 1-element string lists.
func propToString(prop interface{}) string {
	switch v := prop.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		if list := propToStringSlice(prop); len(list) > 0 {
			return list[0]
		}
		return ""
	}
}
