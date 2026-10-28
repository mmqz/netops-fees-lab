package importer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/workbook"
)

// Summary is the outcome of an executed import.
type Summary struct {
	// BatchID is the executed batch.
	BatchID string `json:"batchId"`
	// Status is the batch status after execution (completed).
	Status domain.BatchStatus `json:"status"`
	// RowsTotal counts the data rows of all import sheets.
	RowsTotal int `json:"rowsTotal"`
	// RowsImported counts rows that created a resource.
	RowsImported int `json:"rowsImported"`
	// RowsSkipped counts rows skipped as duplicates of earlier batches.
	RowsSkipped int `json:"rowsSkipped"`
	// RowsFailed counts rows rejected by row-phase error rules (E…).
	RowsFailed int `json:"rowsFailed"`
	// IssuesBySeverity counts the row-phase issues of this batch per
	// severity (set-level issues of the validation engine are excluded —
	// they carry a NULL batch_id).
	IssuesBySeverity map[domain.Severity]int `json:"issuesBySeverity"`
}

// Execute runs the confirmed mapping m against the workbook recorded on
// batch batchID. The pipeline per PROJECT_BRIEF §8:
//
//   - the batch must be awaiting_mapping; the workbook is re-read from the
//     stored path and the mapping validated against the actual headers — a
//     validation failure leaves the batch awaiting_mapping and returns a
//     typed *mapping.FieldError (the user must fix the mapping);
//   - every data row of every import sheet is normalized via the §6 schema
//     and the row-phase rules (E001-E004, W001-W004) decide the outcome
//     imported / skipped / failed;
//   - ALL writes of ALL import sheets (resources, relations, source refs,
//     row results, issues, counts, final status) go through ONE
//     transaction; any internal error rolls everything back and the batch
//     is marked failed with zero partial writes;
//   - row-level rule failures are recorded as failed rows and never abort
//     the import — every input row gets an accounting entry.
//
// Set-level validation (E005/E006/I001) runs separately after import; when
// Execute returns, the database is consistent and ready for that recompute.
func (s *Service) Execute(ctx context.Context, batchID string, m mapping.Mapping) (Summary, error) {
	batch, err := s.db.Batches.Get(ctx, batchID)
	if err != nil {
		return Summary{}, fmt.Errorf("importer: load batch %s: %w", batchID, err)
	}
	if batch.Status != domain.BatchStatusAwaitingMapping {
		return Summary{}, fmt.Errorf("importer: execute batch %s: status %s, want %s: %w",
			batchID, batch.Status, domain.BatchStatusAwaitingMapping, domain.ErrConflict)
	}
	path, err := s.db.Batches.WorkbookPath(ctx, batchID)
	if err != nil {
		return Summary{}, fmt.Errorf("importer: workbook path of batch %s: %w", batchID, err)
	}
	if path == "" {
		return Summary{}, fmt.Errorf("importer: batch %s has no recorded workbook path", batchID)
	}
	wb, err := workbook.Read(path)
	if err != nil {
		if mErr := s.db.Batches.MarkFailed(ctx, batchID, err.Error()); mErr != nil {
			return Summary{}, fmt.Errorf("importer: mark batch %s failed: %w (read error: %v)", batchID, mErr, err)
		}
		return Summary{}, fmt.Errorf("importer: re-read workbook of batch %s: %w", batchID, err)
	}

	headersBySheet := make(map[string][]string, len(wb.Sheets))
	byName := wb.SheetsByName()
	for name, sh := range byName {
		headersBySheet[name] = sh.Headers
	}
	if err := mapping.Validate(m, headersBySheet); err != nil {
		// Mapping problems are user errors: the batch stays awaiting_mapping.
		return Summary{}, err
	}

	mappingJSON, err := mapping.Marshal(m)
	if err != nil {
		return Summary{}, fmt.Errorf("importer: encode mapping of batch %s: %w", batchID, err)
	}
	if err := s.db.Batches.SetMapping(ctx, batchID, string(mappingJSON)); err != nil {
		return Summary{}, fmt.Errorf("importer: store mapping of batch %s: %w", batchID, err)
	}
	if err := s.db.Batches.UpdateStatus(ctx, batchID, domain.BatchStatusImporting); err != nil {
		return Summary{}, fmt.Errorf("importer: mark batch %s importing: %w", batchID, err)
	}

	counts := repo.BatchCounts{}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		c, err := s.executeSheets(ctx, tx, batch, wb, m)
		if err != nil {
			return err
		}
		counts = c
		return nil
	}); err != nil {
		if mErr := s.db.Batches.MarkFailed(ctx, batchID, err.Error()); mErr != nil {
			return Summary{}, fmt.Errorf("importer: mark batch %s failed: %w (import error: %v)", batchID, mErr, err)
		}
		return Summary{}, fmt.Errorf("importer: execute batch %s: %w", batchID, err)
	}

	issueCounts, err := s.db.Issues.CountBySeverity(ctx, &batchID)
	if err != nil {
		return Summary{}, fmt.Errorf("importer: count issues of batch %s: %w", batchID, err)
	}
	if issueCounts == nil {
		issueCounts = map[domain.Severity]int{}
	}
	return Summary{
		BatchID:          batchID,
		Status:           domain.BatchStatusCompleted,
		RowsTotal:        counts.Total,
		RowsImported:     counts.Imported,
		RowsSkipped:      counts.Skipped,
		RowsFailed:       counts.Failed,
		IssuesBySeverity: issueCounts,
	}, nil
}

// executeSheets processes every action=import sheet inside the surrounding
// transaction and finalizes the batch. RowsTotal counts the data rows of
// import sheets ONLY — skipped sheets (explicit or absent from the mapping)
// contribute nothing.
func (s *Service) executeSheets(ctx context.Context, tx *repo.Tx, batch domain.ImportBatch,
	wb *workbook.Workbook, m mapping.Mapping) (repo.BatchCounts, error) {

	byName := wb.SheetsByName()
	st := &sheetState{
		tx:    tx,
		batch: batch,
		now:   s.clock(),
		seen:  map[string]seenEntry{},
		local: map[domain.ResourceType]map[string]string{},
	}
	counts := repo.BatchCounts{}
	for i := range m.Sheets {
		sm := &m.Sheets[i]
		if sm.Action != mapping.ActionImport {
			continue
		}
		sheet, ok := byName[sm.Sheet]
		spec, specOK := mapping.SpecByType(sm.ResourceType)
		keyNames := spec.NaturalKeyNames()
		if !ok || !specOK || len(keyNames) == 0 {
			// Validate already guarantees these; defensive only.
			return counts, fmt.Errorf("importer: sheet %q (type %s) failed validation", sm.Sheet, sm.ResourceType)
		}
		st.spec = spec
		st.sheet = sheet
		st.sm = sm
		st.nameField = spec.NameField()

		results := make([]domain.RowResult, 0, len(sheet.Rows))
		for _, row := range sheet.Rows {
			if err := st.processRow(ctx, row, &results); err != nil {
				return counts, fmt.Errorf("importer: sheet %q row %d: %w", sheet.Name, row.Number, err)
			}
		}
		if err := tx.RowResults.InsertBatch(ctx, results); err != nil {
			return counts, fmt.Errorf("importer: sheet %q: record row results: %w", sheet.Name, err)
		}
		for _, rr := range results {
			switch rr.Status {
			case domain.RowStatusImported:
				counts.Imported++
			case domain.RowStatusSkipped:
				counts.Skipped++
			case domain.RowStatusFailed:
				counts.Failed++
			}
		}
		counts.Total += sheet.DataRows

		if s.afterSheet != nil {
			if err := s.afterSheet(sheet.Name); err != nil {
				return counts, fmt.Errorf("importer: failure after sheet %q: %w", sheet.Name, err)
			}
		}
	}
	if err := tx.Batches.UpdateCounts(ctx, batch.ID, counts); err != nil {
		return counts, fmt.Errorf("importer: update counts of batch %s: %w", batch.ID, err)
	}
	if err := tx.Batches.UpdateStatus(ctx, batch.ID, domain.BatchStatusCompleted); err != nil {
		return counts, fmt.Errorf("importer: complete batch %s: %w", batch.ID, err)
	}
	return counts, nil
}

// seenEntry records the first row of a natural key seen in this batch, so a
// later row with the same key fails E001 with the conflicting row number.
// Both imported AND skipped rows register — a re-import run sees the first
// (skipped) copy of a duplicated key, and the second copy must fail E001
// deterministically, exactly like the first run.
type seenEntry struct {
	rowNumber  int
	resourceID string
}

// sheetState carries the per-execute (seen, local) and per-sheet context of
// row processing.
type sheetState struct {
	tx    *repo.Tx
	batch domain.ImportBatch
	now   time.Time
	seen  map[string]seenEntry
	local map[domain.ResourceType]map[string]string

	spec      mapping.TypeSpec
	sheet     *workbook.Sheet
	sm        *mapping.SheetMapping
	nameField string
}

// pendingWarning is a row-phase warning (W001-W004) collected while the
// row is processed. It becomes an issue only if the row is imported — a
// failed or skipped row records nothing (idempotent re-import therefore
// never re-records warnings of skipped rows).
type pendingWarning struct {
	code     string
	msg      string
	evidence map[string]string
}

// resolvedRef is a ref field whose value resolved to an existing resource.
type resolvedRef struct {
	field    string
	targetID string
}

// processRow normalizes one workbook row via the mapping and schema, runs
// the row-phase rules and appends the row result. The returned error is
// reserved for internal (storage) failures — rule failures are recorded as
// failed rows and return nil.
func (st *sheetState) processRow(ctx context.Context, row workbook.Row, results *[]domain.RowResult) error {
	vals := st.mappedValues(row)
	// Repeated header row (v0.3.1): multi-segment exports (the real telecom
	// sheets) splice the header row back into the data area. Skip it before
	// any rule runs — it is not a data row and must not fail E002/E004.
	if st.isRepeatedHeaderRow(row) {
		*results = append(*results, domain.RowResult{
			BatchID:   st.batch.ID,
			Sheet:     st.sheet.Name,
			RowNumber: row.Number,
			Status:    domain.RowStatusSkipped,
			Message:   "跳过重复表头行（多段导出拼接）",
		})
		return nil
	}
	// Combined 经纬度 split (device/port sheets — the geo field exists in
	// no other schema): when the combined column is mapped and the split
	// 纬度/经度 columns are not both provided from their own columns, derive
	// geo_lat/geo_lng now, BEFORE the E004 loop, so the derived values pass
	// through the same KindLat/KindLng format checks as directly-mapped
	// columns. An unparseable combined value is left un-split and fails
	// E004 on the geo field below.
	splitGeoCombined(vals)
	rt := st.spec.Type.String()
	var warns []pendingWarning

	// E002: every required field must be present — the natural key field(s)
	// first (spec field order; key fields lead every schema, so single-key
	// types keep their historic error order), then the other required
	// fields. Key values are collected on the way for the composite join.
	var keyVals []string
	for _, f := range st.spec.Fields {
		if f.Required && vals[f.Name] == "" {
			return st.failRow(ctx, row, results, domain.RuleMissingRequiredField,
				fmt.Sprintf("必填字段 %s.%s 为空", rt, f.Name),
				map[string]string{"type": rt, "field": f.Name, "raw_value": ""})
		}
		if f.NaturalKey {
			keyVals = append(keyVals, vals[f.Name])
		}
	}
	// B8 key normalization (W21, §12-B8): KeyNormalize types (the 25 W21
	// code-keyed types) normalize EVERY key component before the join —
	// trim (already done by mappedValues, idempotent), fullwidth→halfwidth
	// and upper. Leading zeros are preserved (nothing pads or strips), so
	// "1" and "01" stay distinct identities while " gy-001" / "gy-001" /
	// "ＧＹ-００１" collapse to one. Pre-W21 types keep the raw keys
	// (KeyNormalize false → zero behavior change, B4).
	if st.spec.KeyNormalize {
		for i, v := range keyVals {
			keyVals[i] = mapping.NormalizeNaturalKeyComponent(v)
		}
	}
	// Composite natural key (v0.3.1): key values joined by "|" in spec
	// field order. A single-key type joins one element — the joined key is
	// exactly the historic single-field key, so device/ip_pool/circuit
	// behave (and hash) identically. Port's key is
	// "<device_name>|<port_name>" (migration v8 rebased the device side
	// from device_ip; the IP lives on as a required ipv4 value column).
	key := strings.Join(keyVals, "|")

	// Conditional natural key (v0.4, W004): a type whose primary key may
	// legitimately be empty declares KeyFallbackFields. While every
	// primary key value is non-empty, the joined key stands (the classic
	// path above). When ANY primary key value is empty, the key is rebuilt
	// from the fallback fields' values in declared order, SKIPPING empty
	// ones (a sheet without the scope column — e.g. the real 无线基站
	// export carries no 地市 column — degrades the key to the bare
	// station name), and a W004 warning records the identity-key
	// degradation (W level: the row still imports). E001 dedup and the
	// idempotency lookup below run on the FINAL key, so a coded row and
	// a name-fallback row are distinct identities by construction. The
	// mechanism is spec-driven — no type or field name is matched here
	// (base_station is merely the first user).
	if len(st.spec.KeyFallbackFields) > 0 {
		var missing []string
		keyNames := st.spec.NaturalKeyNames() // parallel to keyVals (spec order)
		for i, v := range keyVals {
			if v == "" {
				missing = append(missing, keyNames[i])
			}
		}
		if len(missing) > 0 {
			parts := make([]string, 0, len(st.spec.KeyFallbackFields))
			for _, name := range st.spec.KeyFallbackFields {
				if v := vals[name]; v != "" {
					parts = append(parts, v)
				}
			}
			key = strings.Join(parts, "|")
			if key == "" {
				// Defensive: the row has no identity at all — the
				// primary key field is empty AND every fallback
				// field is empty. Unreachable for base_station
				// (its name fallback field is Required and E002
				// has already failed such rows); fail as E002 on
				// the primary key field that triggered the
				// degradation.
				return st.failRow(ctx, row, results, domain.RuleMissingRequiredField,
					fmt.Sprintf("身份键为空：%s.%s 缺失且兜底字段（%s）均为空", rt, missing[0],
						strings.Join(st.spec.KeyFallbackFields, ", ")),
					map[string]string{"type": rt, "field": missing[0], "raw_value": ""})
			}
			warns = append(warns, pendingWarning{
				code: domain.RuleKeyFallback,
				msg: fmt.Sprintf("身份键退化：%s.%s 缺失，使用兜底键 %q（兜底字段：%s）",
					rt, strings.Join(missing, "/"), key, strings.Join(st.spec.KeyFallbackFields, ", ")),
				evidence: map[string]string{
					"type":            rt,
					"natural_key":     key,
					"missing_fields":  strings.Join(missing, ","),
					"fallback_fields": strings.Join(st.spec.KeyFallbackFields, ","),
				},
			})
		}
	}

	// E004 / W002: value formats. A bad ipv4/cidr/int fails the row; an
	// unknown enum (status) value is a warning — the row imports, the status
	// value is simply not stored.
	status := ""
	for _, f := range st.spec.Fields {
		v := vals[f.Name]
		if v == "" {
			continue
		}
		switch f.Kind {
		case mapping.KindIPv4, mapping.KindCIDR, mapping.KindInt, mapping.KindLat, mapping.KindLng, mapping.KindGeo:
			// Field-level check = kind parse check + int/float range
			// constraint (vlan_id 1..4094, bandwidth_mbps > 0, lat
			// -90..90, lng -180..180) of PROJECT_BRIEF §6.
			if reason, ok := f.CheckFormat(v); !ok {
				return st.failRow(ctx, row, results, domain.RuleInvalidFormat,
					fmt.Sprintf("字段格式无效：%s.%s=%q（%s）", rt, f.Name, v, reason),
					map[string]string{"type": rt, "field": f.Name, "raw_value": v, "reason": reason})
			}
		case mapping.KindEnum:
			if !f.EnumContains(v) {
				warns = append(warns, pendingWarning{
					code: domain.RuleUnknownStatusValue,
					msg:  fmt.Sprintf("状态值未知：%s.%s=%q（允许：%s）", rt, f.Name, v, strings.Join(f.Enum, "/")),
					evidence: map[string]string{
						"type": rt, "field": f.Name, "raw_value": v,
						"allowed": strings.Join(f.Enum, "/"),
					},
				})
			} else if f.Name == "status" {
				// Only the status FIELD feeds Resource.Status. The KindEnum family
				// grew beyond status in #38 (site_nature/power_mode/lifecycle on the
				// site-like specs; delay_mechanism on ptp_port predates it) — before
				// the guard the LAST mapped enum field with a value silently won,
				// so a mapped delay_mechanism or a recognized 站址性质 could hijack
				// Resource.Status. The trio projects onto its OWN structured columns
				// below; delay_mechanism was always attribute-only by design.
				status = v
			}
		}
	}

	// Refs: E003 when a non-empty ref cannot be resolved (batch-local map ∪
	// database); W001 when an optional ref column is MAPPED but this row's
	// value is empty. W21 refinement: an UNMAPPED optional ref no longer
	// fires W001 — an absent column is not an empty reference, and the W21
	// optional ref columns added to the EXISTING specs (device.machine_
	// room_id/ring_code/igp_domain/serve_customer, port.board_id/
	// vpn_instance, circuit.carried_och/carried_timeslot, base_station.
	// serve_customer/uplink_device) would otherwise fire one warning per
	// row on every pre-W21 workbook, breaking the B4 byte-identical import
	// guarantee. A mapped-but-empty cell still warns (the ip_pool.
	// assigned_device orphan semantics, unchanged).
	var refs []resolvedRef
	for _, f := range st.spec.Fields {
		if f.Kind != mapping.KindRef {
			continue
		}
		v := vals[f.Name]
		if v == "" {
			if !f.Required {
				if _, mapped := st.sm.ColumnMapping[f.Name]; mapped {
					warns = append(warns, pendingWarning{
						code:     domain.RuleOrphanRecord,
						msg:      fmt.Sprintf("可选引用为空：%s.%s", rt, f.Name),
						evidence: map[string]string{"type": rt, "field": f.Name},
					})
				}
			}
			continue
		}
		id, found, err := st.resolveRef(ctx, f, v)
		if err != nil {
			return err
		}
		if !found {
			return st.failRow(ctx, row, results, domain.RuleInvalidReference,
				fmt.Sprintf("引用不存在：%s.%s=%q（目标类型 %s）", rt, f.Name, v, f.RefType),
				map[string]string{"type": rt, "field": f.Name, "ref_value": v, "expected_type": f.RefType.String()})
		}
		refs = append(refs, resolvedRef{field: f.Name, targetID: id})
	}

	// W003: circuit self-loop (a_end == z_end, both non-empty and resolved).
	if st.spec.Type == domain.ResourceTypeCircuit {
		av, zv := vals["a_end_device"], vals["z_end_device"]
		if av != "" && av == zv {
			warns = append(warns, pendingWarning{
				code: domain.RuleCircuitSelfLoop,
				msg:  fmt.Sprintf("电路自环：%s 的 A端与Z端相同（%s）", key, av),
				evidence: map[string]string{
					"circuit_code": key, "device": av,
				},
			})
		}
	}

	// E001: duplicate natural key within this batch.
	ck := st.spec.Type.String() + "\x00" + key
	if prev, dup := st.seen[ck]; dup {
		existingID := ""
		if existing, found, err := st.lookupExisting(ctx, st.spec.Type, key); err != nil {
			return err
		} else if found {
			existingID = existing.ID
		}
		evidence := map[string]string{
			"type":                   rt,
			"natural_key":            key,
			"conflicting_sheet":      st.sheet.Name,
			"conflicting_row_number": strconv.Itoa(prev.rowNumber),
		}
		if existingID != "" {
			evidence["existing_entity_id"] = existingID
		}
		return st.failRow(ctx, row, results, domain.RuleDuplicateKey,
			fmt.Sprintf("重复键 %s/%s：与本批次第 %d 行冲突", rt, key, prev.rowNumber),
			evidence)
	}

	// Idempotency: the key exists from an EARLIER batch ⇒ skipped, no
	// mutation of business data, no issue (the row result carries the
	// message). The only write is the SourceRef version row below —
	// provenance, not a data change.
	if existing, found, err := st.lookupExisting(ctx, st.spec.Type, key); err != nil {
		return err
	} else if found {
		// SourceRef versioning (v0.5 W7-D): the skipped row still
		// CONFIRMS this key against the current batch's
		// workbook/sheet/row, so append one version row — the full
		// provenance of "which workbook last confirmed this key"
		// instead of only the first batch. The UNIQUE
		// (entity_kind, entity_id, batch_id) constraint keeps it at
		// most one row per batch (a second same-key row in the same
		// batch fails E001 above and never reaches this point), and
		// the batch_id ON DELETE CASCADE removes the version with a
		// rollback — zero schema change. The insert mirrors the
		// insert path's srcRef below verbatim. Placed BEFORE the row
		// result append (both are inside the batch transaction, so
		// the ordering is semantically equivalent — an insert
		// failure aborts the whole batch either way) to keep the
		// "evidence written for a skip" (diff + version) grouped
		// together. Purely additive provenance: row result
		// (still skipped), counts, issues and the E001 rule code
		// are all unchanged.
		skipRef := domain.SourceRef{
			ID:         domain.NewID("src"),
			EntityKind: domain.EntityKindResource,
			EntityID:   existing.ID,
			BatchID:    st.batch.ID,
			Workbook:   st.batch.WorkbookName,
			Sheet:      st.sheet.Name,
			RowNumber:  row.Number,
			Raw:        row.Values,
		}
		if err := st.tx.SourceRefs.Insert(ctx, skipRef); err != nil {
			return err
		}
		*results = append(*results, domain.RowResult{
			BatchID:   st.batch.ID,
			Sheet:     st.sheet.Name,
			RowNumber: row.Number,
			Status:    domain.RowStatusSkipped,
			EntityID:  existing.ID,
			RuleCode:  domain.RuleDuplicateKey,
			Message:   fmt.Sprintf("重复导入：资源已存在于批次 %s", existing.FirstBatchID),
		})
		st.seen[ck] = seenEntry{rowNumber: row.Number}
		return nil
	}

	// Import the row: resource + source ref + relations (each with its own
	// source ref) + row result, then the pending warnings as issues.
	resource := domain.Resource{
		ID:           domain.NewID("res"),
		Type:         st.spec.Type,
		NaturalKey:   key,
		Name:         st.resourceName(vals, key),
		Attributes:   st.attributes(vals),
		Status:       status,
		FirstBatchID: st.batch.ID,
		CreatedAt:    st.now,
		UpdatedAt:    st.now,
	}
	// W30 (issue #45): the geo split columns also land as REAL columns on
	// the row — the SQL-level spatial substrate of migration v20. The values
	// already passed the KindLat/KindLng format checks above (E004 row
	// failure), so a parse miss here cannot happen for mapped geo columns;
	// the guard stays defensive rather than trusting that invariant.
	if v := vals["geo_lat"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			resource.GeoLat = &f
		}
	}
	if v := vals["geo_lng"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			resource.GeoLng = &f
		}
	}
	// #38 站址主数据: the three enum columns project onto the structured
	// migration-v40 columns the cross-dataset reconcile's attribution reads.
	// The values already passed the KindEnum check above — an out-of-set
	// value warned W002 there and lands NOWHERE (column NULL, attributes
	// skip enum fields: the row imports, never a guess); a recognized value
	// re-reads the SAME closed set here (siteNatureEnum & co. are the
	// parser's own spellings, pinned in lockstep by site_master_test).
	if v := vals["site_nature"]; v != "" {
		if n, ok := domain.ParseSiteNatureValue(v); ok {
			resource.SiteNature = string(n)
		}
	}
	if v := vals["power_mode"]; v != "" {
		if p, ok := domain.ParsePowerModeValue(v); ok {
			resource.PowerMode = string(p)
		}
	}
	if v := vals["lifecycle"]; v != "" {
		if l, ok := domain.ParseLifecycleValue(v); ok {
			resource.Lifecycle = string(l)
		}
	}
	if err := st.tx.Resources.Insert(ctx, resource); err != nil {
		return err
	}
	srcRef := domain.SourceRef{
		ID:         domain.NewID("src"),
		EntityKind: domain.EntityKindResource,
		EntityID:   resource.ID,
		BatchID:    st.batch.ID,
		Workbook:   st.batch.WorkbookName,
		Sheet:      st.sheet.Name,
		RowNumber:  row.Number,
		Raw:        row.Values,
	}
	if err := st.tx.SourceRefs.Insert(ctx, srcRef); err != nil {
		return err
	}
	for _, r := range refs {
		rel := domain.Relation{
			ID:             domain.NewID("rel"),
			Type:           mapping.RelationTypeFor(st.spec.Type, r.field),
			FromResourceID: resource.ID,
			ToResourceID:   r.targetID,
			Attributes:     map[string]string{},
			FirstBatchID:   st.batch.ID,
			CreatedAt:      st.now,
		}
		if err := st.tx.Relations.Insert(ctx, rel); err != nil {
			return err
		}
		relRef := domain.SourceRef{
			ID:         domain.NewID("src"),
			EntityKind: domain.EntityKindRelation,
			EntityID:   rel.ID,
			BatchID:    st.batch.ID,
			Workbook:   st.batch.WorkbookName,
			Sheet:      st.sheet.Name,
			RowNumber:  row.Number,
			Raw:        row.Values,
		}
		if err := st.tx.SourceRefs.Insert(ctx, relRef); err != nil {
			return err
		}
	}
	*results = append(*results, domain.RowResult{
		BatchID:   st.batch.ID,
		Sheet:     st.sheet.Name,
		RowNumber: row.Number,
		Status:    domain.RowStatusImported,
		EntityID:  resource.ID,
	})
	for _, w := range warns {
		severity, ok := domain.SeverityForCode(w.code)
		if !ok {
			return fmt.Errorf("importer: unknown rule code %q", w.code)
		}
		if err := st.tx.Issues.Insert(ctx, domain.Issue{
			ID:          domain.NewID("iss"),
			BatchID:     &st.batch.ID,
			RuleCode:    w.code,
			RuleVersion: domain.RuleVersion,
			Severity:    severity,
			EntityKind:  domain.EntityKindResource,
			EntityID:    resource.ID,
			SourceRefID: &srcRef.ID,
			Message:     w.msg,
			Evidence:    w.evidence,
			CreatedAt:   st.now,
		}); err != nil {
			return err
		}
	}

	st.seen[ck] = seenEntry{rowNumber: row.Number, resourceID: resource.ID}
	if st.local[st.spec.Type] == nil {
		st.local[st.spec.Type] = map[string]string{}
	}
	st.local[st.spec.Type][key] = resource.ID

	return nil
}

// splitGeoCombined fills vals["geo_lat"]/vals["geo_lng"] from the combined
// 经纬度 value of vals["geo"] using mapping.SplitGeoLatLng's China-bounds
// heuristic. It is a no-op unless the geo column is mapped with a
// non-empty value — which can only happen on a device sheet, the one
// schema carrying the geo fields — and the split 纬度/经度 columns are not
// BOTH already provided from their own mapped columns (explicit split
// columns win; a partially-provided pair is completed from the combined
// value so lat/lng always come from one consistent source). The raw geo
// attribute is kept alongside the split values — harmless, and it keeps
// the original import value traceable in the resource attributes.
func splitGeoCombined(vals map[string]string) {
	if vals["geo"] == "" || (vals["geo_lat"] != "" && vals["geo_lng"] != "") {
		return
	}
	lat, lng, ok := mapping.SplitGeoLatLng(vals["geo"])
	if !ok {
		return // unparseable: leave it for the E004 format check on the geo field
	}
	vals["geo_lat"] = mapping.FormatGeoFloat(lat)
	vals["geo_lng"] = mapping.FormatGeoFloat(lng)
}

// isRepeatedHeaderRow reports whether row repeats the sheet's header row
// verbatim (multi-segment export concatenation, e.g. row 3 of the real
// telecom port sheet): at least 3 non-empty cells and EVERY non-empty cell
// equal to its column header. A genuine data row whose values merely
// coincide with a few headers stays below the 3-cell bar (fully-empty rows
// never reach the importer); a header row of another export style (header
// B of the multi-header sheet) differs in at least one mapped cell and
// flows through the normal rules, failing E002/E004 with clear evidence.
func (st *sheetState) isRepeatedHeaderRow(row workbook.Row) bool {
	nonEmpty := 0
	for header, v := range row.Values {
		if v == "" {
			continue
		}
		nonEmpty++
		if v != header {
			return false
		}
	}
	return nonEmpty >= 3
}

// mappedValues extracts the trimmed raw value of every mapped target field
// of the row (mapping: target field ← source header).
func (st *sheetState) mappedValues(row workbook.Row) map[string]string {
	vals := make(map[string]string, len(st.sm.ColumnMapping))
	for field, header := range st.sm.ColumnMapping {
		if v, ok := row.Values[header]; ok {
			vals[field] = strings.TrimSpace(v)
		}
	}
	return vals
}

// resourceName returns the Name of the resource: the name-ish field value
// (device_name / pool_name / port_name; circuit falls to the key), falling
// back to the natural key.
func (st *sheetState) resourceName(vals map[string]string, key string) string {
	if v := vals[st.nameField]; v != "" {
		return v
	}
	return key
}

// attributes returns the normalized non-empty values of every mapped field
// except the natural key and the enum (status) fields — the status value
// lives in Resource.Status (and is dropped entirely when unknown, W002).
// For a conditional-key type the primary key field (base_station_code) is
// excluded like any key field, while the FALLBACK fields (city,
// base_station_name) are ordinary fields and stay visible in attrs — the
// key components of a degraded identity remain inspectable on the resource.
func (st *sheetState) attributes(vals map[string]string) map[string]string {
	attrs := map[string]string{}
	for _, f := range st.spec.Fields {
		if f.NaturalKey || f.Kind == mapping.KindEnum {
			continue
		}
		if v := vals[f.Name]; v != "" {
			attrs[f.Name] = v
		}
	}
	return attrs
}

// lookupExisting finds the resource of the given type and natural key in
// the current transaction (batch-local inserts included).
func (st *sheetState) lookupExisting(ctx context.Context, rt domain.ResourceType, key string) (domain.Resource, bool, error) {
	res, err := st.tx.Resources.GetByTypeKey(ctx, rt, key)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Resource{}, false, nil
		}
		return domain.Resource{}, false, err
	}
	return res, true, nil
}

// resolveRef resolves a ref value against the batch-local imported map ∪
// the database (via the current transaction). When the TARGET type is a
// B8 KeyNormalize type the value is normalized first (§12-B8: the target's
// keys are stored normalized, so " gy-001" / "ＧＹ-００１" resolve to the
// same resource as "GY-001"; the failure message keeps the RAW value —
// E003 evidence shows what the sheet actually said). Targets of pre-W21
// types match raw, exactly as before.
func (st *sheetState) resolveRef(ctx context.Context, f mapping.FieldSpec, v string) (string, bool, error) {
	if spec, ok := mapping.SpecByType(f.RefType); ok && spec.KeyNormalize {
		v = mapping.NormalizeNaturalKeyComponent(v)
	}
	if m := st.local[f.RefType]; m != nil {
		if id, ok := m[v]; ok {
			return id, true, nil
		}
	}
	res, found, err := st.lookupExisting(ctx, f.RefType, v)
	if err != nil || !found {
		return "", false, err
	}
	return res.ID, true, nil
}

// failRow records a failed row result and the matching row-phase issue with
// evidence {sheet, row_number, …}. Failed rows create no entity, so the
// issue keeps source_ref_id NULL and locates its origin via the evidence.
func (st *sheetState) failRow(ctx context.Context, row workbook.Row, results *[]domain.RowResult,
	code, msg string, evidence map[string]string) error {

	severity, ok := domain.SeverityForCode(code)
	if !ok {
		return fmt.Errorf("importer: unknown rule code %q", code)
	}
	ev := make(map[string]string, len(evidence)+2)
	ev["sheet"] = st.sheet.Name
	ev["row_number"] = strconv.Itoa(row.Number)
	for k, v := range evidence {
		ev[k] = v
	}
	*results = append(*results, domain.RowResult{
		BatchID:   st.batch.ID,
		Sheet:     st.sheet.Name,
		RowNumber: row.Number,
		Status:    domain.RowStatusFailed,
		RuleCode:  code,
		Message:   msg,
	})
	return st.tx.Issues.Insert(ctx, domain.Issue{
		ID:          domain.NewID("iss"),
		BatchID:     &st.batch.ID,
		RuleCode:    code,
		RuleVersion: domain.RuleVersion,
		Severity:    severity,
		Message:     msg,
		Evidence:    ev,
		CreatedAt:   st.now,
	})
}
