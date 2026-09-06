package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// GraphHandler exposes the Neo4j knowledge-graph operations over HTTP so
// external consumers (the MCP gateway, scripts, integrations) can add,
// delete, and search graph data with the same tenant/KB authorization as
// the rest of the API.
type GraphHandler struct {
	graphService *service.GraphService
}

// NewGraphHandler creates a GraphHandler.
func NewGraphHandler(graphService *service.GraphService) *GraphHandler {
	return &GraphHandler{graphService: graphService}
}

// AddGraph godoc
// @Summary      Add knowledge graph data (nodes + relationships) to Neo4j
// @Description  Write a graph payload for the given knowledge base. When
//               knowledge_id is provided the nodes are scoped to that single
//               knowledge/file; otherwise they are scoped to the KB. Nodes are
//               merged by (name, kg) with chunk lists unioned; relationships
//               are merged via apoc.merge.relationship.
// @Tags         Graph
// @Accept       json
// @Produce      json
// @Param        kb_id  path      string  true  "Knowledge base ID"
// @Param        body   body      graphAddRequest  true  "Graph data + optional knowledge scope"
// @Success      200    {object}  map[string]interface{}
// @Failure      400    {object}  map[string]interface{}
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/graph/write [post]
func (h *GraphHandler) AddGraph(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("kb_id"))
	if kbID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge base ID is required"})
		return
	}

	var req graphAddRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if len(req.Graphs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "graphs must not be empty"})
		return
	}

	ns := types.NameSpace{KnowledgeBase: kbID}
	if req.KnowledgeID != "" {
		ns.Knowledge = req.KnowledgeID
	}

	if err := h.graphService.AddGraph(c.Request.Context(), ns, req.Graphs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "nodes": graphNodeCount(req.Graphs), "relations": graphRelCount(req.Graphs)})
}

// DelGraph godoc
// @Summary      Delete knowledge graph data from Neo4j
// @Description  Delete the graph scoped to the KB. Pass knowledge_id to delete
//               only that file's subgraph; omit it to delete the whole KB graph.
// @Tags         Graph
// @Produce      json
// @Param        kb_id         path    string  false  "Knowledge base ID"
// @Param        knowledge_id  query   string  false  "Scope deletion to one knowledge/file"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/graph/write [delete]
func (h *GraphHandler) DelGraph(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("kb_id"))
	if kbID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge base ID is required"})
		return
	}

	ns := types.NameSpace{KnowledgeBase: kbID}
	if kid := c.Query("knowledge_id"); kid != "" {
		ns.Knowledge = kid
	}

	if err := h.graphService.DelGraph(c.Request.Context(), []types.NameSpace{ns}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SearchNode godoc
// @Summary      Search Neo4j graph nodes by name
// @Description  Return the induced subgraph of nodes whose name CONTAINS any
//               of the query terms (comma/space separated), plus the edges
//               between them, scoped to the knowledge base.
// @Tags         Graph
// @Produce      json
// @Param        kb_id  path    string  true   "Knowledge base ID"
// @Param        q      query   string  true   "Node name fragment(s) to search"
// @Success      200  {object}  types.GraphData
// @Failure      400  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /knowledgebase/{kb_id}/wiki/graph/node [get]
func (h *GraphHandler) SearchNode(c *gin.Context) {
	kbID := secutils.SanitizeForLog(c.Param("kb_id"))
	if kbID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge base ID is required"})
		return
	}
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter q is required"})
		return
	}

	ns := types.NameSpace{KnowledgeBase: kbID}
	graph, err := h.graphService.SearchNode(c.Request.Context(), ns, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if graph == nil {
		graph = &types.GraphData{}
	}
	c.JSON(http.StatusOK, graph)
}

type graphAddRequest struct {
	KnowledgeID string             `json:"knowledge_id"`
	Graphs      []*types.GraphData `json:"graphs"`
}

func graphNodeCount(graphs []*types.GraphData) int {
	n := 0
	for _, g := range graphs {
		if g != nil {
			n += len(g.Node)
		}
	}
	return n
}

func graphRelCount(graphs []*types.GraphData) int {
	n := 0
	for _, g := range graphs {
		if g != nil {
			n += len(g.Relation)
		}
	}
	return n
}
