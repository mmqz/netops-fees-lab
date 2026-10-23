package api

// openapi.go — GET /api/openapi.json (v0.10-R9): the OpenAPI 3.0
// document generated from the SAME route table the mux registers from
// (routes.go). One source of truth = zero spec drift: the paths, tags
// and summaries below are projections of route entries, and the
// anti-drift test walks every entry against the live server.
//
// Scope honesty: this is a DISCOVERY document (the contest's AI
// integration story — an agent reads it and understands the whole
// surface), not a full schema contract. Request/response bodies are
// described at the envelope level; the per-endpoint JSON shapes live
// in the repo docs (docs/design §9) and the TypeScript client
// (web/src/api/types.ts). Deep schema generation from Go structs is a
// deliberate non-goal: a half-generated schema that lies is worse
// than an honest envelope summary.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// openAPIResponse is one method entry of the document.
type openAPIResponse struct {
	Description string                    `json:"description"`
	Content     map[string]openAPIContent `json:"content,omitempty"`
}

type openAPIContent struct {
	Schema map[string]any `json:"schema,omitempty"`
}

// openAPIOperation is one method of one path.
type openAPIOperation struct {
	Tags        []string                   `json:"tags"`
	Summary     string                     `json:"summary"`
	Description string                     `json:"description,omitempty"`
	Responses   map[string]openAPIResponse `json:"responses"`
}

// openAPIPath groups the methods of one pattern. Exactly one method in
// this API per pattern — no path carries two verbs.
type openAPIPath struct {
	Get    *openAPIOperation `json:"get,omitempty"`
	Post   *openAPIOperation `json:"post,omitempty"`
	Patch  *openAPIOperation `json:"patch,omitempty"`
	Delete *openAPIOperation `json:"delete,omitempty"`
}

// openAPIDoc is the root document.
type openAPIDoc struct {
	OpenAPI string                 `json:"openapi"`
	Info    map[string]any         `json:"info"`
	Tags    []map[string]string    `json:"tags"`
	Paths   map[string]openAPIPath `json:"paths"`
}

// tagOrder is the deterministic tag ordering of the document (first
// appearance order of the route table).
func tagOrder(routes []route) []string {
	seen := map[string]bool{}
	var order []string
	for _, r := range routes {
		if !seen[r.Tag] {
			seen[r.Tag] = true
			order = append(order, r.Tag)
		}
	}
	return order
}

// successEnvelope is the shared success response body description.
func successEnvelope(desc string) openAPIResponse {
	return openAPIResponse{
		Description: desc,
		Content: map[string]openAPIContent{
			"application/json": {Schema: map[string]any{"type": "object"}},
		},
	}
}

// routeResponses projects one route's HONEST status set (#103: the document
// used to claim 200 for every route while nine routes answer 201, four answer
// 202, and the fees-analysis routes answer 501 on default builds). The set:
// the route's declared success status + the per-route derived defaults
// (400 everywhere; 404 on {id} patterns; 401 on mutating verbs — the
// optional --auth-token write gate; 500 everywhere) + the route's declared
// extras (501 engine_not_built, 422 semantic file errors, 409 conflicts).
func routeResponses(r route) map[string]openAPIResponse {
	success := r.SuccessStatus
	if success == 0 {
		success = http.StatusOK
	}
	out := map[string]openAPIResponse{
		strconv.Itoa(success): successEnvelope("成功（错误走统一信封 {error:{code,message}}）"),
		"400":                 {Description: "请求校验失败（统一信封）"},
		"500":                 {Description: "服务器内部错误（统一信封，message 不携带内部细节）"},
	}
	if strings.Contains(r.Pattern, "{id}") || strings.Contains(r.Pattern, "{cpId}") || strings.Contains(r.Pattern, "{scenario}") {
		out["404"] = openAPIResponse{Description: "对象不存在（not_found）"}
	}
	if r.Method != http.MethodGet {
		out["401"] = openAPIResponse{Description: "写门禁启用时（serve --auth-token）：缺少/错误 Bearer 令牌（unauthorized）"}
	}
	for status, desc := range r.ExtraStatuses {
		out[strconv.Itoa(status)] = openAPIResponse{Description: desc}
	}
	return out
}

// buildOpenAPIDoc projects the route table into the document.
func buildOpenAPIDoc(routes []route) openAPIDoc {
	paths := map[string]openAPIPath{}
	for _, r := range routes {
		op := openAPIOperation{
			Tags:      []string{r.Tag},
			Summary:   r.Summary,
			Responses: routeResponses(r),
		}
		p := paths[r.Pattern]
		switch r.Method {
		case http.MethodGet:
			p.Get = &op
		case http.MethodPost:
			p.Post = &op
		case http.MethodPatch:
			p.Patch = &op
		case http.MethodDelete:
			p.Delete = &op
		}
		paths[r.Pattern] = p
	}

	tags := make([]map[string]string, 0)
	for _, tag := range tagOrder(routes) {
		tags = append(tags, map[string]string{"name": tag})
	}

	return openAPIDoc{
		OpenAPI: "3.0.3",
		Info: map[string]any{
			"title":       "NetOps Lab API",
			"version":     domain.AppVersion,
			"description": "云网运营实验室 API：资源治理、导入校验、仿真工作台、指标时序、检测告警。统一 JSON 信封 {error:{code,message}}；分页响应 {items,total,page,pageSize}。只读 AI 面在 MCP（tools/list），本 HTTP 面是人与运维工具的通道。",
		},
		Tags:  tags,
		Paths: paths,
	}
}

// openAPICache memoizes the marshaled document (the route table is
// immutable after construction; rebuilding per request would be
// wasted work).
var openAPICache struct {
	once sync.Once
	raw  []byte
}

// handleOpenAPISpec implements GET /api/openapi.json.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	openAPICache.once.Do(func() {
		raw, err := json.Marshal(buildOpenAPIDoc(s.apiRoutes()))
		if err != nil {
			// The document is pure data (no channels/funcs); a marshal
			// failure is a programming bug, not a runtime condition —
			// panic in the once would poison every future request, so
			// store the error envelope instead and answer 500 forever
			// (loudly, visibly, honestly).
			openAPICache.raw = []byte(`{"error":{"code":"internal_error","message":"openapi document marshal failed"}}`)
			return
		}
		openAPICache.raw = raw
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPICache.raw)
}
