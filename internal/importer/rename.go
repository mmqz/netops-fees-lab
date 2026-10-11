package importer

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
)

// This file implements the W006 suspected-rename detection (v0.5 W6-D, the
// closing move of natural-keys.md §6 gap #3): "name change = identity
// change event" needs a human confirmation, never an automatic merge.
//
// The detection runs on the INSERT branch of a row of a rename-detection
// type (mapping.TypeSpec.RenameDetection — the name-keyed types
// ip_pool/base_station/port; code-keyed types are excluded on doctrine: a
// changed code IS another resource). It is the mirror twin of W005's
// diff-skip path: W005 fires when the key is the SAME and the data changed
// (row skips, diff recorded); W006 fires when the key is NEW and the data
// did not change (row imports as a new resource, rename proposal recorded).
// Together they close the two halves of "re-import with changes".
//
// TWO-PHASE design: the per-type key universe must be complete before any
// judgment ("old key vanished from the source" is only decidable once
// every row of every sheet has been parsed). Phase 1 happens implicitly —
// processRow registers the FINAL key of every successfully parsed row
// (imported AND skipped) in sheetState.seen, exactly the E001 dedup map;
// rows that fail row-phase rules never reach that checkpoint. Phase 2
// (detectRenames) runs after ALL import sheets have been processed, inside
// the SAME db.Do transaction as the row results and the counts — a
// half-written rename proposal is impossible.

// importedRowInfo captures one imported row of a rename-detection type for
// the phase-2 pass: the new resource's identity, its parsed values, and the
// origin coordinates. Rows of non-detection types are never collected (the
// insert branch filters on spec.RenameDetection).
type importedRowInfo struct {
	typ         domain.ResourceType
	key         string
	resourceID  string
	sourceRefID string
	vals        map[string]string
	sheet       string
	rowNumber   int
	// derived names the fields of vals whose values were COMPUTED during
	// row processing rather than read from a mapped column — the geo split
	// (geo_lat/geo_lng filled from the combined 经纬度 value). Computed
	// fields are excluded from the attribute-equality match: their
	// representation depends on the derivation path (a direct column
	// "45.750" and a split of "126.63,45.75" both exist as attributes but
	// would not compare equal), and a coordinate is not identity evidence
	// for a rename hypothesis anyway.
	derived map[string]struct{}
}

// detectRenames runs the phase-2 suspected-rename detection over every row
// this execution imported from a rename-detection type. For each type it:
//
//  1. extracts the type's key universe from st.seen (every key a
//     successfully parsed row of the batch produced — the "rename = old
//     name vanished from the source" precondition);
//  2. enumerates the OLD candidates — same-type resources in the database
//     whose natural key is NOT in the universe (resources created by this
//     batch are in the universe by construction and never compete);
//  3. matches each imported row against each candidate on every match
//     field (non-key, non-fallback-key, non-ref, non-enum, non-computed;
//     normalized empty == absent, the same value-gathering as
//     attributes()/recordAttributeDiff — a second value semantics is never
//     invented);
//  4. requires a BIJECTION: exactly one candidate matches the row AND that
//     candidate matches no other imported row; anything ambiguous (0 or ≥2
//     on either side) stays SILENT — no issue, no proposal (conservative
//     silence beats a fuzzy hint);
//  5. requires at least one NON-EMPTY matched field (a name-only table
//     with empty attributes everywhere must not produce hollow proposals);
//  6. records the W006 warning issue on the imported row's new resource
//     and inserts the pending rename proposal.
//
// Determinism: types are visited in type-name order, rows in import order,
// candidates in (natural_key, id) order — the emitted issues and proposals
// have a stable order for a given workbook + database state.
func (st *sheetState) detectRenames(ctx context.Context) error {
	if len(st.imported) == 0 {
		return nil
	}
	types := make([]domain.ResourceType, 0, 2)
	byType := make(map[domain.ResourceType][]importedRowInfo)
	for _, row := range st.imported {
		if _, known := byType[row.typ]; !known {
			types = append(types, row.typ)
		}
		byType[row.typ] = append(byType[row.typ], row)
	}
	sort.Slice(types, func(i, j int) bool { return types[i].String() < types[j].String() })

	for _, typ := range types {
		spec, ok := mapping.SpecByType(typ)
		if !ok || !spec.RenameDetection {
			continue // defensive: collection already filtered, registry agrees
		}
		matchFields := renameMatchFields(spec)

		// Phase-1 result: the type's key universe (st.seen registers the
		// final key of every parsed row — imported AND skipped).
		prefix := typ.String() + "\x00"
		universe := make(map[string]struct{})
		for ck := range st.seen {
			if strings.HasPrefix(ck, prefix) {
				universe[strings.TrimPrefix(ck, prefix)] = struct{}{}
			}
		}

		// Old candidates: same-type resources whose key vanished from the
		// source sheets of this batch.
		all, err := st.tx.Resources.ListByType(ctx, typ)
		if err != nil {
			return fmt.Errorf("importer: rename detection (%s): %w", typ, err)
		}
		candidates := make([]domain.Resource, 0)
		for _, res := range all {
			if _, inSheet := universe[res.NaturalKey]; !inSheet {
				candidates = append(candidates, res)
			}
		}
		if len(candidates) == 0 {
			continue
		}

		rows := byType[typ]
		// Bijection bookkeeping: matches per row / per candidate, the
		// single candidate of a uniquely-matched row, and the matched
		// fields per (row, candidate) pair.
		rowMatches := make([]int, len(rows))
		candMatches := make([]int, len(candidates))
		rowCand := make([]int, len(rows))
		matchedFields := make(map[[2]int][]string, len(rows))
		for i, row := range rows {
			rowCand[i] = -1
			for j, cand := range candidates {
				equal, matched := renameRowMatchesCandidate(matchFields, row, cand)
				if !equal {
					continue
				}
				rowMatches[i]++
				candMatches[j]++
				if rowMatches[i] == 1 {
					rowCand[i] = j
				} else {
					rowCand[i] = -1 // ambiguous row — never proposes
				}
				matchedFields[[2]int{i, j}] = matched
			}
		}
		for i, row := range rows {
			j := rowCand[i]
			if rowMatches[i] != 1 || j < 0 || candMatches[j] != 1 {
				continue // bijection broken — conservative silence
			}
			matched := matchedFields[[2]int{i, j}]
			if len(matched) == 0 {
				continue // hollow match (all match fields empty) — silence
			}
			cand := candidates[j]
			if err := st.recordRenameProposal(ctx, row, cand, matched); err != nil {
				return err
			}
		}
	}
	return nil
}

// renameMatchFields returns the fields that participate in the
// attribute-equality match of a rename hypothesis: every spec field EXCEPT
// the natural-key fields, the conditional-key fallback fields (the
// rename-suspect name components themselves), enum fields (status — the
// attributes()/diff gathering excludes them too), ref fields (a ref value
// is relation evidence, not attribute identity) and computed fields (the
// geo split — filtered per row via importedRowInfo.derived).
func renameMatchFields(spec mapping.TypeSpec) []mapping.FieldSpec {
	fallback := make(map[string]struct{}, len(spec.KeyFallbackFields))
	for _, name := range spec.KeyFallbackFields {
		fallback[name] = struct{}{}
	}
	var fields []mapping.FieldSpec
	for _, f := range spec.Fields {
		if f.NaturalKey || f.Kind == mapping.KindEnum || f.Kind == mapping.KindRef {
			continue
		}
		if _, isFallback := fallback[f.Name]; isFallback {
			continue
		}
		fields = append(fields, f)
	}
	return fields
}

// renameRowMatchesCandidate reports whether the imported row and the
// candidate old resource are equal on EVERY match field, normalized the
// same way the importer stores attributes (empty == absent; an unmapped
// column yields "" and a stored-only value yields a mismatch — a sheet that
// stopped mapping a column does not silently "match"). The second return
// lists the fields with non-empty equal values (the proposal's evidence;
// empty when the whole match is hollow).
func renameRowMatchesCandidate(fields []mapping.FieldSpec, row importedRowInfo, cand domain.Resource) (bool, []string) {
	matched := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, computed := row.derived[f.Name]; computed {
			continue
		}
		nv := row.vals[f.Name]        // "" when the column is unmapped
		ov := cand.Attributes[f.Name] // "" when never stored (absent)
		if nv != ov {
			return false, nil
		}
		if nv != "" {
			matched = append(matched, f.Name)
		}
	}
	return true, matched
}

// recordRenameProposal writes the W006 warning issue (on the imported row's
// new resource, with its source ref — the row DID import) and the pending
// rename proposal (anchored at the OLD resource, the identity keeper).
// Both writes join the execution transaction; the row's imported status and
// the batch counts are unaffected.
func (st *sheetState) recordRenameProposal(ctx context.Context, row importedRowInfo,
	cand domain.Resource, matched []string) error {

	rt := row.typ.String()
	if err := st.tx.RenameProposals.Insert(ctx, domain.RenameProposal{
		ID:            domain.NewID("rp"),
		ResourceID:    cand.ID,
		NewResourceID: row.resourceID,
		BatchID:       st.batch.ID,
		ResourceType:  row.typ,
		OldKey:        cand.NaturalKey,
		NewKey:        row.key,
		MatchedFields: matched,
		Status:        domain.RenameStatusPending,
		Sheet:         row.sheet,
		RowNumber:     row.rowNumber,
		CreatedAt:     st.now,
	}); err != nil {
		return fmt.Errorf("importer: record rename proposal %s→%s: %w", cand.NaturalKey, row.key, err)
	}
	srcRefID := row.sourceRefID
	return st.tx.Issues.Insert(ctx, domain.Issue{
		ID:          domain.NewID("iss"),
		BatchID:     &st.batch.ID,
		RuleCode:    domain.RuleSuspectedRename,
		RuleVersion: domain.RuleVersion,
		Severity:    domain.SeverityWarning,
		EntityKind:  domain.EntityKindResource,
		EntityID:    row.resourceID,
		SourceRefID: &srcRefID,
		Message: fmt.Sprintf("疑似改名待确认：%s/%s 疑似是 %s 改名后的新资源（%d 个属性字段一致，行已按新资源导入，请人工确认是否合并身份）",
			rt, row.key, cand.NaturalKey, len(matched)),
		Evidence: map[string]string{
			"type":            rt,
			"old_key":         cand.NaturalKey,
			"new_key":         row.key,
			"resource_id":     cand.ID,
			"new_resource_id": row.resourceID,
			"matched_fields":  strings.Join(matched, ","),
			"matched_count":   strconv.Itoa(len(matched)),
			"sheet":           row.sheet,
			"row_number":      strconv.Itoa(row.rowNumber),
		},
		CreatedAt: st.now,
	})
}

// derivedFields reports which fields of vals carry COMPUTED values rather
// than values read from mapped columns: geo_lat/geo_lng when their columns
// are not directly mapped but splitGeoCombined filled them from the
// combined 经纬度 value (the only derivation in today's row pipeline).
// Computed fields are excluded from the rename match (see
// importedRowInfo.derived).
func (st *sheetState) derivedFields(vals map[string]string) map[string]struct{} {
	var derived map[string]struct{}
	for _, name := range []string{"geo_lat", "geo_lng"} {
		if vals[name] == "" {
			continue
		}
		if _, mapped := st.sm.ColumnMapping[name]; mapped {
			continue // a directly mapped split column is a source value
		}
		if derived == nil {
			derived = map[string]struct{}{}
		}
		derived[name] = struct{}{}
	}
	return derived
}
