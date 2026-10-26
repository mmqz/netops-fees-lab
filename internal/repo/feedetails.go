package repo

// feedetails.go — the fee_detail_lines persistence (migration v38, #16's
// completion line item 6). The detail layer is the manual-entry evidence
// tier: it never touches the system-table amounts, and the delta between
// the two tiers is a read-side computation (差额只提示，不阻断).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// FeeDetailRepo persists the manual detail rows.
type FeeDetailRepo struct{ q DBTX }

const feeDetailColumns = `id, link_key, period, fee_type, category, amount_cents, occurred_on, item_name, quantity, quantity_unit, unit_price_cents, reason, location, work_order_no, invoice_no, counterparty, source_file, source_row, remark, status, created_by, created_at, verified_by, verified_at, reject_reason`

// Insert adds one detail row. Status flows through the service (pending on
// entry); the provenance columns are the caller's contract.
func (r *FeeDetailRepo) Insert(ctx context.Context, d domain.FeeDetail) error {
	_, err := r.q.ExecContext(ctx, `INSERT INTO fee_detail_lines
(id, link_key, period, fee_type, category, amount_cents, occurred_on, item_name, quantity, quantity_unit, unit_price_cents, reason, location, work_order_no, invoice_no, counterparty, source_file, source_row, remark, status, created_by, created_at, verified_by, verified_at, reject_reason)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.LinkKey, d.Period, string(d.FeeType), d.Category, d.AmountCents,
		nullableString(d.OccurredOn), nullableString(d.ItemName), nullableFloatPtr(d.Quantity),
		nullableString(d.QuantityUnit), nullableInt64(d.UnitPriceCents),
		nullableString(d.Reason), nullableString(d.Location), nullableString(d.WorkOrderNo),
		nullableString(d.InvoiceNo), nullableString(d.Counterparty), nullableString(d.SourceFile),
		nullableInt(int64(d.SourceRow)), nullableString(d.Remark),
		d.Status, d.CreatedBy, formatTime(d.CreatedAt), nullableString(d.VerifiedBy),
		nullableTime(d.VerifiedAt), nullableString(d.RejectReason))
	if err != nil {
		return fmt.Errorf("repo: insert fee detail %s: %w", d.ID, err)
	}
	return nil
}

// ExactDuplicate answers the id of an existing row that matches the entry
// fingerprint (link triple + category + amount + item name) — the spec's
// 铁律 5 (同一笔明细不允许重复关联) made operator-facing: the service
// refuses with the existing id instead of letting a double-entry silently
// double-count.
func (r *FeeDetailRepo) ExactDuplicate(ctx context.Context, d domain.FeeDetail) (string, error) {
	var id string
	err := r.q.QueryRowContext(ctx, `SELECT id FROM fee_detail_lines
WHERE link_key = ? AND period = ? AND fee_type = ? AND category = ? AND amount_cents = ? AND COALESCE(item_name,'') = ?`,
		d.LinkKey, d.Period, string(d.FeeType), d.Category, d.AmountCents, d.ItemName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: duplicate probe fee detail: %w", err)
	}
	return id, nil
}

// List answers the rows matching the filter (newest first — the entry
// ledger reads like a journal).
func (r *FeeDetailRepo) List(ctx context.Context, f domain.FeeDetailFilter) ([]domain.FeeDetail, error) {
	where := []string{}
	args := []any{}
	if f.LinkKey != "" {
		where = append(where, "link_key = ?")
		args = append(args, f.LinkKey)
	}
	if f.Period != "" {
		where = append(where, "period = ?")
		args = append(args, f.Period)
	}
	if f.FeeType != "" {
		where = append(where, "fee_type = ?")
		args = append(args, string(f.FeeType))
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Category != "" {
		where = append(where, "category = ?")
		args = append(args, f.Category)
	}
	query := `SELECT ` + feeDetailColumns + ` FROM fee_detail_lines`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY created_at DESC, id`
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list fee details: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.FeeDetail
	for rows.Next() {
		d, err := scanFeeDetail(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetByID answers one row or an error wrapping domain.ErrNotFound.
func (r *FeeDetailRepo) GetByID(ctx context.Context, id string) (domain.FeeDetail, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+feeDetailColumns+` FROM fee_detail_lines WHERE id = ?`, id)
	d, err := scanFeeDetail(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.FeeDetail{}, fmt.Errorf("repo: fee detail %s: %w", id, domain.ErrNotFound)
	}
	return d, err
}

// SetStatus flips the human-channel status (confirmed/rejected/unmatched)
// with its audit columns — the spec's 铁律 3/7 (核实留痕：谁/何时/什么
// 原因). The caller writes the reason; verified_at is now.
func (r *FeeDetailRepo) SetStatus(ctx context.Context, id, status, verifiedBy, rejectReason string, at time.Time) error {
	res, err := r.q.ExecContext(ctx, `UPDATE fee_detail_lines
SET status = ?, verified_by = ?, verified_at = ?, reject_reason = ? WHERE id = ?`,
		status, verifiedBy, formatTime(at), nullableString(rejectReason), id)
	if err != nil {
		return fmt.Errorf("repo: set fee detail %s status %s: %w", id, status, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: set fee detail %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: fee detail %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// SumCents answers Σ amount_cents of the link triple (the evidence-side
// total of the reconcile computation; the system-table side lives in the
// Parquet analysis layer and is the engine's to answer).
func (r *FeeDetailRepo) SumCents(ctx context.Context, linkKey, period string, feeType domain.FeeType) (int64, int, error) {
	var total sql.NullInt64
	var n int
	err := r.q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_cents),0), COUNT(*) FROM fee_detail_lines WHERE link_key = ? AND period = ? AND fee_type = ?`,
		linkKey, period, string(feeType)).Scan(&total, &n)
	if err != nil {
		return 0, 0, fmt.Errorf("repo: sum fee details (%s %s): %w", linkKey, period, err)
	}
	return total.Int64, n, nil
}

// nullable helpers mirror the fees-domain SQL discipline: empty strings
// and zero ints persist as NULL (absent, not faked).
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableFloatPtr(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return formatTime(*p)
}

func scanFeeDetail(row interface{ Scan(dest ...any) error }) (domain.FeeDetail, error) {
	var d domain.FeeDetail
	var occurredOn, itemName, quantityUnit, reason, location, workOrderNo, invoiceNo,
		counterparty, sourceFile, remark, verifiedBy, rejectReason sql.NullString
	var quantity sql.NullFloat64
	var unitPrice sql.NullInt64
	var sourceRow sql.NullInt64
	var verifiedAt sql.NullString
	var createdAt sql.NullString
	if err := row.Scan(&d.ID, &d.LinkKey, &d.Period, &d.FeeType, &d.Category, &d.AmountCents,
		&occurredOn, &itemName, &quantity, &quantityUnit, &unitPrice, &reason, &location,
		&workOrderNo, &invoiceNo, &counterparty, &sourceFile, &sourceRow, &remark,
		&d.Status, &d.CreatedBy, &createdAt, &verifiedBy, &verifiedAt, &rejectReason); err != nil {
		return d, err
	}
	d.OccurredOn = occurredOn.String
	d.ItemName = itemName.String
	if quantity.Valid {
		v := quantity.Float64
		d.Quantity = &v
	}
	d.QuantityUnit = quantityUnit.String
	if unitPrice.Valid {
		v := unitPrice.Int64
		d.UnitPriceCents = &v
	}
	d.Reason = reason.String
	d.Location = location.String
	d.WorkOrderNo = workOrderNo.String
	d.InvoiceNo = invoiceNo.String
	d.Counterparty = counterparty.String
	d.SourceFile = sourceFile.String
	d.SourceRow = int(sourceRow.Int64)
	d.Remark = remark.String
	if createdAt.Valid {
		t, err := parseTime(createdAt.String)
		if err != nil {
			return d, err
		}
		d.CreatedAt = t
	}
	d.VerifiedBy = verifiedBy.String
	if verifiedAt.Valid {
		t, err := parseTime(verifiedAt.String)
		if err != nil {
			return d, err
		}
		d.VerifiedAt = &t
	}
	d.RejectReason = rejectReason.String
	return d, nil
}
