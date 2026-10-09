package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/workbook"
)

// Analysis is the result of Analyze: the workbook structure, per-column
// suggestions and a full suggested mapping. No resources are written.
type Analysis struct {
	// BatchID is the created import batch (status awaiting_mapping).
	BatchID string `json:"batchId"`
	// WorkbookName is the base file name of the workbook.
	WorkbookName string `json:"workbookName"`
	// SHA256 is the hex-encoded hash of the workbook bytes.
	SHA256 string `json:"sha256"`
	// Sheets lists every sheet of the workbook in workbook order.
	Sheets []SheetAnalysis `json:"sheets"`
	// SuggestedMapping is the prefilled mapping model for the UI.
	SuggestedMapping mapping.Mapping `json:"suggestedMapping"`
}

// SheetAnalysis describes one analyzed worksheet.
type SheetAnalysis struct {
	// Sheet is the worksheet name.
	Sheet string `json:"sheet"`
	// RowCount is the number of non-empty data rows (Excel rows 2+).
	RowCount int `json:"rowCount"`
	// SkippedEmptyRows counts interior fully-empty data rows.
	SkippedEmptyRows int `json:"skippedEmptyRows"`
	// Columns lists the header-row columns in sheet order.
	Columns []ColumnAnalysis `json:"columns"`
	// SuggestedType is the resource type suggested from the sheet name;
	// "" when the name carries no hint.
	SuggestedType domain.ResourceType `json:"suggestedType,omitempty"`
}

// ColumnAnalysis describes one workbook column.
type ColumnAnalysis struct {
	// Header is the trimmed header cell text.
	Header string `json:"header"`
	// Samples holds up to 3 non-empty values of the column, in row order.
	Samples []string `json:"samples"`
	// SuggestedField is the suggested target field; "" when none.
	SuggestedField string `json:"suggestedField"`
	// Confidence is the suggestion confidence; 0 when none.
	Confidence float64 `json:"confidence"`
}

// maxSamples caps the per-column sample values reported by Analyze.
const maxSamples = 3

// Analyze parses the workbook at path, records an import batch
// (analyzing → awaiting_mapping) with its hash, rule version and workbook
// path, and returns the workbook structure plus suggestions. Nothing is
// written to resources.
//
// A readable but unparseable workbook (e.g. not an xlsx container) marks the
// batch failed and returns a typed error wrapping the workbook package's
// sentinel. A missing/unreadable file returns an error without creating a
// batch (the workbook cannot even be identified or hashed).
func (s *Service) Analyze(ctx context.Context, path string) (Analysis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Analysis{}, fmt.Errorf("importer: read workbook %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	batch := domain.ImportBatch{
		ID:             domain.NewID("bat"),
		WorkbookName:   filepath.Base(path),
		WorkbookSHA256: hex.EncodeToString(sum[:]),
		Status:         domain.BatchStatusAnalyzing,
		RuleVersion:    domain.RuleVersion,
		StartedAt:      s.clock(),
	}
	if err := s.db.Batches.Create(ctx, batch); err != nil {
		return Analysis{}, fmt.Errorf("importer: create batch %s: %w", batch.ID, err)
	}
	if err := s.db.Batches.SetWorkbookPath(ctx, batch.ID, path); err != nil {
		return Analysis{}, fmt.Errorf("importer: record workbook path of batch %s: %w", batch.ID, err)
	}

	wb, err := workbook.Read(path)
	if err != nil {
		if mErr := s.db.Batches.MarkFailed(ctx, batch.ID, err.Error()); mErr != nil {
			return Analysis{}, fmt.Errorf("importer: mark batch %s failed: %w (parse error: %v)", batch.ID, mErr, err)
		}
		return Analysis{}, fmt.Errorf("importer: analyze %s: %w", batch.WorkbookName, err)
	}

	infos := make([]mapping.SheetInfo, len(wb.Sheets))
	for i := range wb.Sheets {
		infos[i] = mapping.SheetInfo{Name: wb.Sheets[i].Name, Headers: wb.Sheets[i].Headers}
	}
	a := Analysis{
		BatchID:          batch.ID,
		WorkbookName:     wb.Name,
		SHA256:           wb.SHA256,
		SuggestedMapping: mapping.SuggestMapping(infos),
	}
	for i := range wb.Sheets {
		sh := &wb.Sheets[i]
		sa := SheetAnalysis{
			Sheet:            sh.Name,
			RowCount:         sh.DataRows,
			SkippedEmptyRows: sh.SkippedEmptyRows,
		}
		if t, ok := mapping.SuggestType(sh.Name); ok {
			sa.SuggestedType = t
		}
		suggestions := mapping.Suggest(sh.Name, sh.Headers)
		for ci, h := range sh.Headers {
			sa.Columns = append(sa.Columns, ColumnAnalysis{
				Header:         h,
				Samples:        columnSamples(sh, ci, maxSamples),
				SuggestedField: suggestions[ci].Field,
				Confidence:     suggestions[ci].Confidence,
			})
		}
		a.Sheets = append(a.Sheets, sa)
	}

	if err := s.db.Batches.SetMapping(ctx, batch.ID, ""); err != nil {
		return Analysis{}, fmt.Errorf("importer: reset mapping of batch %s: %w", batch.ID, err)
	}
	if err := s.db.Batches.UpdateStatus(ctx, batch.ID, domain.BatchStatusAwaitingMapping); err != nil {
		return Analysis{}, fmt.Errorf("importer: mark batch %s awaiting mapping: %w", batch.ID, err)
	}
	return a, nil
}

// columnSamples collects up to max non-empty values of the headerIdx-th
// column, in row order.
func columnSamples(sheet *workbook.Sheet, headerIdx, max int) []string {
	samples := make([]string, 0, max)
	if headerIdx >= len(sheet.Headers) {
		return samples
	}
	header := sheet.Headers[headerIdx]
	for _, row := range sheet.Rows {
		if v := row.Values[header]; v != "" {
			samples = append(samples, v)
			if len(samples) == max {
				break
			}
		}
	}
	return samples
}
