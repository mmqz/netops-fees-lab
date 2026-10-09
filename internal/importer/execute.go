package importer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/workbook"
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
//     and the row-phase rules (E001-E004, W001-W003) decide the outcome
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
		keyField, keyOK := spec.NaturalKeyField()
		if !ok || !specOK || !keyOK {
			// Validate already guarantees these; defensive only.
			return counts, fmt.Errorf("importer: sheet %q (type %s) failed validation", sm.Sheet, sm.ResourceType)
		}
		st.spec = spec
		st.sheet = sheet
		st.sm = sm
		st.keyField = keyField.Name
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
	keyField  string
	nameField string
}

// pendingWarning is a row-phase warning (W001/W002/W003) collected while the
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
	key := vals[st.keyField]
	rt := st.spec.Type.String()
	var warns []pendingWarning

	// E002: the natural key must be present.
	if key == "" {
		return st.failRow(ctx, row, results, domain.RuleMissingRequiredField,
			fmt.Sprintf("必填字段 %s.%s 为空", rt, st.keyField),
			map[string]string{"type": rt, "field": st.keyField, "raw_value": ""})
	}
	// E002: every other required field must be present.
	for _, f := range st.spec.Fields {
		if f.Required && !f.NaturalKey && vals[f.Name] == "" {
			return st.failRow(ctx, row, results, domain.RuleMissingRequiredField,
				fmt.Sprintf("必填字段 %s.%s 为空", rt, f.Name),
				map[string]string{"type": rt, "field": f.Name, "raw_value": ""})
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
		case mapping.KindIPv4, mapping.KindCIDR, mapping.KindInt:
			// Field-level check = kind parse check + int range constraint
			// (vlan_id 1..4094, bandwidth_mbps > 0) of PROJECT_BRIEF §6.
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
			} else {
				status = v
			}
		}
	}

	// Refs: E003 when a non-empty ref cannot be resolved (batch-local map ∪
	// database); W001 when an optional ref is empty.
	var refs []resolvedRef
	for _, f := range st.spec.Fields {
		if f.Kind != mapping.KindRef {
			continue
		}
		v := vals[f.Name]
		if v == "" {
			if !f.Required {
				warns = append(warns, pendingWarning{
					code:     domain.RuleOrphanRecord,
					msg:      fmt.Sprintf("可选引用为空：%s.%s", rt, f.Name),
					evidence: map[string]string{"type": rt, "field": f.Name},
				})
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
	// mutation, no issue (the row result carries the message).
	if existing, found, err := st.lookupExisting(ctx, st.spec.Type, key); err != nil {
		return err
	} else if found {
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
			Type:           relationTypeFor(st.spec.Type, r.field),
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
// (device_name / pool_name / circuit_code), falling back to the natural key.
func (st *sheetState) resourceName(vals map[string]string, key string) string {
	if v := vals[st.nameField]; v != "" {
		return v
	}
	return key
}

// attributes returns the normalized non-empty values of every mapped field
// except the natural key and the enum (status) fields — the status value
// lives in Resource.Status (and is dropped entirely when unknown, W002).
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
// the database (via the current transaction).
func (st *sheetState) resolveRef(ctx context.Context, f mapping.FieldSpec, v string) (string, bool, error) {
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

// relationTypeFor maps a ref field of a resource type to the derived
// relation type of PROJECT_BRIEF §6: ip_pool.assigned_device → assigned_to,
// circuit.a_end_device → a_end, circuit.z_end_device → z_end.
func relationTypeFor(rt domain.ResourceType, field string) domain.RelationType {
	switch {
	case rt == domain.ResourceTypeIPPool && field == "assigned_device":
		return domain.RelationTypeAssignedTo
	case rt == domain.ResourceTypeCircuit && field == "a_end_device":
		return domain.RelationTypeAEnd
	case rt == domain.ResourceTypeCircuit && field == "z_end_device":
		return domain.RelationTypeZEnd
	default:
		return domain.RelationType("")
	}
}
