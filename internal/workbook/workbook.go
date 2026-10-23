// Package workbook reads .xlsx files into a simple sheet/row model.
//
// It is the only consumer of excelize for parsing (besides the test data
// generator). Responsibilities: compute the workbook's SHA-256, walk all
// sheets, normalize the header row (row 1, trimmed, trailing empty headers
// ignored), keep the REAL 1-based Excel row number of every data row
// (row 1 is the header, data starts at row 2), skip fully-empty rows, and
// trim trailing empty rows.
//
// Since v0.5 W8-D the package also fronts the PTP configuration-script
// channel: a .txt/.cfg file whose content carries the PTP script
package workbook

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ErrNotXLSX is returned (wrapped) when the file cannot be opened as an
// xlsx/zip container.
var ErrNotXLSX = errors.New("workbook: not a valid xlsx file")

// ErrEmptyWorkbook is returned when the file parses but contains no sheet
// with at least one data row.
var ErrEmptyWorkbook = errors.New("workbook: no sheet with data rows")

// ErrWorkbookTooLarge is returned (wrapped) when the workbook exceeds one
// of the parse limits: more than MaxRowsPerSheet rows in one sheet, or more
// than MaxCellsPerWorkbook materialized cells across all sheets. The
// compressed upload size limit (10MB in the HTTP API) says nothing about
// the DECOMPRESSED size — a highly compressed xlsx could otherwise expand
// to memory-hostile dimensions (zip bomb), because every cell of every
// sheet used to be materialized at once.
var ErrWorkbookTooLarge = errors.New("workbook: exceeds size limits")

// Parse limits (zip-bomb guard). Package-level variables so tests can lower
// them; production code only reads them. A limit <= 0 disables that bound.
var (
	// MaxRowsPerSheet bounds the number of worksheet rows (including the
	// header row) materialized for one sheet.
	MaxRowsPerSheet = 100_000
	// MaxCellsPerWorkbook bounds the total number of materialized cells
	// across all sheets of the workbook.
	MaxCellsPerWorkbook = 5_000_000
)

// Workbook is a parsed .xlsx file.
type Workbook struct {
	// Name is the base file name, for example "devices.xlsx".
	Name string
	// SHA256 is the hex-encoded SHA-256 of the file bytes.
	SHA256 string
	// Sheets holds every sheet of the file in workbook order, including
	// sheets without data rows.
	Sheets []Sheet
}

// Sheet is one parsed worksheet.
type Sheet struct {
	// Name is the worksheet name, for example "设备清单".
	Name string
	// Headers are the trimmed header cells of Excel row 1 with trailing
	// empty headers removed. An interior empty header stays as "" (its
	// column values are then keyed by "").
	Headers []string
	// Rows are the non-empty data rows (Excel rows 2+), keeping their real
	// 1-based Excel row numbers.
	Rows []Row
	// DataRows is len(Rows): the number of non-empty data rows.
	DataRows int
	// SkippedEmptyRows counts interior fully-empty data rows. Trailing
	// empty rows are trimmed and NOT counted.
	SkippedEmptyRows int
}

// Row is one non-empty data row.
type Row struct {
	// Number is the real 1-based Excel row number (row 1 is the header,
	// data rows start at 2). Import accounting and issue evidence must
	// reference this number.
	Number int
	// Values maps header text to the trimmed cell value. Every header key
	// is present; missing or blank cells map to "".
	Values map[string]string
}

// Read parses the .xlsx file at path — or, when the extension is .txt/.cfg
// AND the content carries the PTP script fingerprint (see
// ptpconfig.IsScriptText), parses the PTP configuration script and
// channel. It returns a workbook whose SHA256 is the hash of the file
// bytes. Errors wrap ErrNotXLSX for unreadable xlsx files, ErrEmptyWorkbook
// for files without any data, ErrWorkbookTooLarge when the file exceeds the
// parse limits.
func Read(path string) (*Workbook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("workbook: read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	wb, err := parseBytes(data)
	if err != nil {
		return nil, err
	}
	wb.Name = filepath.Base(path)
	wb.SHA256 = hex.EncodeToString(sum[:])
	return wb, nil
}

// parseBytes parses xlsx bytes without file metadata. Each sheet is
// streamed (excelize row iterator) under the MaxRowsPerSheet /
// MaxCellsPerWorkbook limits; a breach aborts with ErrWorkbookTooLarge.
func parseBytes(data []byte) (*Workbook, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotXLSX, err)
	}
	defer func() { _ = f.Close() }()
	wb := &Workbook{}
	cells := 0
	for _, name := range f.GetSheetList() {
		raw, used, err := sheetRows(f, name, cells)
		if err != nil {
			return nil, err
		}
		cells += used
		wb.Sheets = append(wb.Sheets, BuildSheet(name, raw))
	}
	// Honest-import guard (#81): excelize v2.11's row iterator swallows
	// mid-stream XML decode errors (Next/Columns discard xml.Decoder
	// errors and Rows.Error only reports its own ErrMaxRows), so a sheet
	// cut short mid-file would otherwise import as a silent row prefix.
	// The tail check is the structural fingerprint that catches the
	// dominant real-world corruption class; it also re-verifies every
	// worksheet stream's zip CRC32 in passing.
	if err := CheckWorksheetTails(data); err != nil {
		return nil, err
	}
	for i := range wb.Sheets {
		if wb.Sheets[i].DataRows > 0 {
			return wb, nil
		}
	}
	return nil, fmt.Errorf("%w: %d sheet(s) parsed", ErrEmptyWorkbook, len(wb.Sheets))
}

// sheetRows streams one worksheet and returns its raw rows exactly as
// excelize's GetRows would (row 1 first, interior gaps as empty slices,
// trailing empty rows trimmed), plus the number of materialized cells. It
// mirrors the GetRows collection loop statement for statement, so parsing
// semantics are identical — but it aborts with ErrWorkbookTooLarge as soon
// as the sheet passes MaxRowsPerSheet rows or the workbook (cellsBefore +
// this sheet) passes MaxCellsPerWorkbook, instead of materializing the
// whole sheet first.
func sheetRows(f *excelize.File, name string, cellsBefore int) ([][]string, int, error) {
	rows, err := f.Rows(name)
	if err != nil {
		return nil, 0, fmt.Errorf("workbook: read sheet %q: %w", name, err)
	}
	results, cur, maxVal, cells := make([][]string, 0, 64), 0, 0, 0
	for rows.Next() {
		cur++
		if MaxRowsPerSheet > 0 && cur > MaxRowsPerSheet {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("%w: sheet %q has more than %d rows", ErrWorkbookTooLarge, name, MaxRowsPerSheet)
		}
		row, err := rows.Columns()
		if err != nil {
			// #81: a mid-row iteration error must NOT read as "file ends
			// here" — returning the prefix as success is silent
			// truncation (the workbook channel's row-count contract is
			// the idempotency baseline).
			_ = rows.Close()
			return nil, 0, fmt.Errorf("workbook: read sheet %q row %d: %w", name, cur, err)
		}
		if len(row) > 0 {
			if emptyRows := cur - maxVal - 1; emptyRows > 0 {
				results = append(results, make([][]string, emptyRows)...)
			}
			results = append(results, row)
			maxVal = cur
			cells += len(row)
			if MaxCellsPerWorkbook > 0 && cellsBefore+cells > MaxCellsPerWorkbook {
				_ = rows.Close()
				return nil, 0, fmt.Errorf("%w: workbook exceeds %d cells while reading sheet %q",
					ErrWorkbookTooLarge, MaxCellsPerWorkbook, name)
			}
		}
	}
	// #81: Rows.Error is the iterator's own error surface (ErrMaxRows);
	// Rows.Close only reports the temp-file close in the normal path.
	if err := rows.Error(); err != nil {
		_ = rows.Close()
		return nil, 0, fmt.Errorf("workbook: read sheet %q: %w", name, err)
	}
	return results[:maxVal], cells, rows.Close()
}

// BuildSheet converts positional raw rows (raw[i] is Excel row i+1, as
// returned by excelize GetRows, which fills interior row gaps with empty
// slices) into a Sheet. A data row whose header-mapped cells are all empty
// or whitespace is skipped: interior ones count in SkippedEmptyRows, all
// rows after the last non-empty row are trimmed (not counted). Values are
// trimmed; cells beyond the header count are ignored. The function is pure
// so tests can drive it with synthetic input.
func BuildSheet(name string, raw [][]string) Sheet {
	var s Sheet
	s.Name = name
	if len(raw) == 0 {
		return s
	}
	headers := make([]string, len(raw[0]))
	for i, h := range raw[0] {
		headers[i] = strings.TrimSpace(h)
	}
	for len(headers) > 0 && headers[len(headers)-1] == "" {
		headers = headers[:len(headers)-1]
	}
	s.Headers = headers
	nHeaders := len(headers)

	last := -1 // index in raw of the last non-empty data row
	for i := 1; i < len(raw); i++ {
		if !rowEmpty(raw[i], nHeaders) {
			last = i
		}
	}
	if last > 0 {
		s.Rows = make([]Row, 0, last)
	}
	for i := 1; i <= last; i++ {
		if rowEmpty(raw[i], nHeaders) {
			s.SkippedEmptyRows++
			continue
		}
		cells := raw[i]
		row := Row{Number: i + 1, Values: make(map[string]string, nHeaders)}
		for c := 0; c < nHeaders; c++ {
			v := ""
			if c < len(cells) {
				v = strings.TrimSpace(cells[c])
			}
			row.Values[headers[c]] = v
		}
		s.Rows = append(s.Rows, row)
	}
	s.DataRows = len(s.Rows)
	return s
}

// rowEmpty reports whether all header-mapped cells of the row are empty or
// whitespace. Cells beyond the header count are unmapped and ignored.
func rowEmpty(cells []string, nHeaders int) bool {
	for c := 0; c < nHeaders && c < len(cells); c++ {
		if strings.TrimSpace(cells[c]) != "" {
			return false
		}
	}
	return true
}

// SheetsByName returns the sheets of the workbook indexed by sheet name.
func (w *Workbook) SheetsByName() map[string]*Sheet {
	m := make(map[string]*Sheet, len(w.Sheets))
	for i := range w.Sheets {
		m[w.Sheets[i].Name] = &w.Sheets[i]
	}
	return m
}

// ErrTruncatedSheet reports an xlsx whose worksheet XML stream ends without
// its close tag — the structural fingerprint of a sheet that was cut short
// or corrupted mid-file (#81). excel's own reader refuses such a file
// outright; without this guard our stream reader would import a silent row
// prefix as success and the caller could not tell "the file had 40 rows"
// from "the file was cut after row 40".
var ErrTruncatedSheet = errors.New("workbook: worksheet stream ends without its close tag (truncated or corrupt xlsx)")

// CheckWorksheetTails verifies that every worksheet XML stream inside the
// xlsx zip ends with the worksheet close tag. It exists because excelize
// v2.11's row iterator has no error surface for mid-stream XML corruption:
// Rows.Next and Rows.Columns both DISCARD xml.Decoder errors (rows.go:105
// "token, _ := rows.decoder.Token()" and rows.go:172), Rows.Error only
// reports excelize's own ErrMaxRows, and Rows.Close only reports the
// temp-file close in the normal path — so a truncated sheet parses as a
// clean row prefix with a nil error everywhere. The tail check closes that
// hole; fully streaming every sheet also re-verifies each entry's zip
// CRC32 (a corrupt stream surfaces as zip.ErrChecksum here).
//
// The check is structural, not semantic: it accepts any well-formed tail
// ending in the close tag, so it costs one sequential pass per worksheet
// (bounded by the same file the caller just parsed) and never false-positives
// on legitimate files — every OOXML producer must close the root element.
// It is exported so the fees channel (issue #81's second instance, whose
// readXLSX shares the same excelize semantics) enforces the identical
// contract.
func CheckWorksheetTails(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotXLSX, err)
	}
	checked := 0
	for _, ze := range zr.File {
		if !strings.HasPrefix(ze.Name, "xl/worksheets/") || !strings.HasSuffix(ze.Name, ".xml") {
			continue
		}
		checked++
		rc, err := ze.Open()
		if err != nil {
			return fmt.Errorf("workbook: open %s: %w", ze.Name, err)
		}
		var tail []byte
		chunk := make([]byte, 32*1024)
		for {
			n, rerr := rc.Read(chunk)
			if n > 0 {
				tail = append(tail, chunk[:n]...)
				if len(tail) > 256 {
					tail = tail[len(tail)-256:]
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				_ = rc.Close()
				return fmt.Errorf("workbook: read %s: %w", ze.Name, rerr)
			}
		}
		_ = rc.Close()
		if !strings.HasSuffix(strings.TrimSpace(string(tail)), "</worksheet>") {
			return fmt.Errorf("%w: %s", ErrTruncatedSheet, ze.Name)
		}
	}
	if checked == 0 {
		// A zip with zero worksheet entries cannot have come through a
		// successful excelize parse (GetSheetList read at least one);
		// reaching here means the file's structure changed under us.
		return fmt.Errorf("%w: no worksheet streams found", ErrTruncatedSheet)
	}
	return nil
}
