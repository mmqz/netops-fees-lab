package api_test

// resources_registry_test.go — #108-A: the HTTP type-validation error of
// GET /api/resources must carry the FULL closed set, derived from the
// domain registry. The pre-#108 message hand-copied the 8 pre-W21 types and
// had been lying to every caller asking for a W21 type since the registry
// extended to 33; a derived join cannot drift, and this gate holds the
// derivation honest (the CLI twin lives in internal/app).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestResourcesTypeErrorListsRegistryClosedSet pins the bad-type 400 to the
// registry, bidirectionally: every registry type must appear in the message,
// nothing else may, and the count is a conscious 33-pin in the four-point
// style — extending the registry fails here until the pin is revisited.
func TestResourcesTypeErrorListsRegistryClosedSet(t *testing.T) {
	srv := newAPITestServer(t)
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources?type=__bogus__", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /api/resources?type=__bogus__: status = %d, want 400", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", string(body), err)
	}
	if env.Error.Code != "bad_request" {
		t.Fatalf("error.code = %q, want bad_request", env.Error.Code)
	}
	msg := env.Error.Message

	// Same-family wording as the CLI/MCP faces (#91 doctrine): the surface
	// says "registered resource type" and carries the derived closed set.
	if !strings.Contains(msg, "type 参数必须是注册资源类型之一（") {
		t.Errorf("message lost the cross-face wording contract: %q", msg)
	}

	// 33-pin (four-point style): mapping's four-point gate holds the
	// registry to the same number; a registry extension must consciously
	// revisit this pin and the derived surfaces.
	all := domain.AllResourceTypes()
	if len(all) != 33 {
		t.Fatalf("domain registry holds %d resource types, want 33 — if the closed set changed, update this pin (and the four-point gate in internal/mapping)", len(all))
	}

	open := strings.Index(msg, "（")
	end := strings.Index(msg, "）")
	if open < 0 || end < open {
		t.Fatalf("message carries no closed-set enumeration: %q", msg)
	}
	claimed := map[string]bool{}
	for _, part := range strings.Split(msg[open+len("（"):end], "/") {
		if part = strings.TrimSpace(part); part != "" {
			claimed[part] = true
		}
	}
	// Direction 1: every registry type is listed — the pre-#108 message
	// omitted all 25 W21 types.
	for _, rt := range all {
		if !claimed[rt.String()] {
			t.Errorf("message omits registry type %q (claimed %d of %d)", rt.String(), len(claimed), len(all))
		}
	}
	// Direction 2: nothing is claimed that the registry does not know.
	if len(claimed) != len(all) {
		t.Errorf("message claims %d types, registry holds %d", len(claimed), len(all))
	}
	// Spot-pins: one pre-W21 type and one W21 type — the stale message
	// carried the first and lied about the second.
	for _, pin := range []string{"ptp_port", "pon_port"} {
		if !claimed[pin] {
			t.Errorf("message omits %q (the 8-type hand-copy regression)", pin)
		}
	}
}
