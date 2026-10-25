// annotations.go — the MCP standard tool annotations of issue #10 (v0.18).
//
// The 2025-06 MCP spec defines ToolAnnotations so a CLIENT can tell a
// read-only search from a state-mutating operation without parsing
// prose descriptions: readOnlyHint ("does not modify its environment"),
// destructiveHint ("may perform destructive updates"), idempotentHint
// ("repeated calls have no additional effect"), openWorldHint ("may
// interact with an open world of external entities"). Issue #10's verdict:
// the tools carried none of them, so an AI client had to guess — and the
// project's own "MCP read-only red line" existed only in comments.
//
// The fee repo's whole surface is pure query, so the behavior contract is
// one row; the consistency test (TestMCPToolAnnotationsMatchBehavior) pins
// annotation ↔ tool-name so a future write-side tool must extend this
// table (and consciously decide its --read-only registration) instead of
// silently inheriting the read-only claims:
//
//	tool               readOnly  destructive  idempotent  rationale
//	────────────────────────────────────────────────────────────────
//	resource_search    true      false        true        pure query
//	fee_risks          true      false        true        pure query
//	fee_totals         true      false        true        pure query
//	fee_duplicates     true      false        true        pure query
//
// openWorldHint is false for every tool: the whole surface operates on the
// local database and analysis tree — no external entities, no network
// calls. All four hints are set explicitly (never left to the SDK
// defaults: a default is an unspoken claim).
package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// hintBool is the *bool constructor of the omitempty hint fields.
func hintBool(v bool) *bool { return &v }

// readOnlyHints returns the annotation set of the pure-query tools: no
// environment modification, safe to re-run, closed local world. All four
// tools of this repo share it today; a tool that does NOT deserve it must
// grow its own hints function here — and the annotations test will fail
// until the table is told the truth.
func readOnlyHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: hintBool(false),
		IdempotentHint:  true,
		OpenWorldHint:   hintBool(false),
	}
}
