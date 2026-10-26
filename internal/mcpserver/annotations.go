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
//	fee_rules_create   false     false        false      #2 phase 1: one
//	                              row in fee_risk_rules, DISABLED on
//	                              arrival (观察态); a repeated call
//	                              with the same name errors (UNIQUE),
//	                              so it is honestly NOT idempotent.
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
// environment modification, safe to re-run, closed local world. The four
// query tools share it; fee_rules_create does NOT (its hints function is
// ruleCreateHints) — a tool that does not deserve the set must grow its
// own hints function here, and the annotations test fails until the
// table is told the truth.
func readOnlyHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: hintBool(false),
		IdempotentHint:  true,
		OpenWorldHint:   hintBool(false),
	}
}

// ruleCreateHints is the annotation set of the #2 write-side tool: NOT
// read-only (a rule row lands), NOT destructive (nothing is deleted or
// overwritten — phase 1 has no update channel; a threshold change is
// disable-old + create-new), NOT idempotent (a repeated identical call
// answers the UNIQUE-name refusal instead of no-op'ing). Registered on
// NewServer ONLY — never on the --read-only variant (the invariant is
// pinned by TestMCPReadOnlyRegistryIsReadOnlyHintSet).
func ruleCreateHints() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: hintBool(false),
		IdempotentHint:  false,
		OpenWorldHint:   hintBool(false),
	}
}
