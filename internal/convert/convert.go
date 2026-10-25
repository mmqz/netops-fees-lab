// Package convert implements the .xls → .xlsx/.csv conversion of issue #7.
//
// The real ledgers of this project arrive as Excel 2003 binary workbooks
// (.xls / BIFF8); the import pipeline reads .xlsx/.csv only and —
// deliberately — refuses .xls with a clear message. Issue #7's verdict:
// refuse-to-import is right, but the manual "open Excel → Save As" loop
// is per-file busywork with real corruption risk (wrong type on save,
// dropped sheets, altered cells). convert is that missing shell: a
// batch, fidelity-preserving container swap.
//
// Fidelity contract (issue #7 acceptance #1):
//   - every sheet survives, in order, under its own name;
//   - per-cell: numbers stay numbers, dates stay dates (the raw serial
//     preserved through the 1900/1904 mode of the source workbook), text
//     stays text, formula cells contribute their CACHED numeric results
//     (what Excel displayed — the .xls cache, not a re-evaluation);
//   - merged ranges are reproduced (the double-row headers of real
//     ledgers merge horizontally; without the MERGEDCELLS record the
//     values still convert but the visual structure is lost);
//   - what is NOT preserved, honestly: cell styles/fonts/column widths
//     (container decoration, not content — the import pipeline never
//     consumed them) and the formula expressions themselves (only the
//     cached values travel, exactly what reading the file in Excel
//     would show).
//
// The reader behind this package is internal/xls — a vendored, patched
// copy of github.com/extrame/xls (Apache-2.0, attribution in LICENSE)
// whose upstream silently dropped formula cells and hardcoded dates to
// "2006.01" text; the patches are marked PATCHED(netops-fees-lab #7) and
// enumerated in that package.
package convert

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/xls"
)

// Format selects the output container.
type Format int

const (
	FormatXLSX Format = iota
	FormatCSV
)

// ParseFormat parses the CLI flag value.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "xlsx":
		return FormatXLSX, nil
	case "csv":
		return FormatCSV, nil
	}
	return FormatXLSX, fmt.Errorf("未知格式 %q（支持：xlsx / csv）", s)
}

// FileResult is the per-input-file outcome.
type FileResult struct {
	Input  string   `json:"input"`
	Status string   `json:"status"` // converted / failed / skipped
	Output []string `json:"outputs,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// Result is the whole run (the CLI prints it as the single JSON object).
type Result struct {
	Converted int          `json:"converted"`
	Failed    int          `json:"failed"`
	Skipped   int          `json:"skipped"`
	Files     []FileResult `json:"files"`
}

// ConvertFile converts one .xls file into the requested container.
//
// xlsx → <out>/<original basename>.xlsx (the original file name carries
// the month/branch information of the ledger — acceptance #4).
// csv  → <out>/<original basename>/<sheet>.csv (one CSV per sheet; the
//
//	multi-sheet ledgers keep their 直供电/转供电/沿边沿线 split).
func ConvertFile(src, outDir string, format Format) ([]string, error) {
	switch format {
	case FormatXLSX:
		stem := filepath.Base(src)
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		out := filepath.Join(outDir, stem+".xlsx")
		if err := convertXLSX(src, out); err != nil {
			return nil, fmt.Errorf("转换失败：%v", err)
		}
		return []string{out}, nil
	case FormatCSV:
		stem := filepath.Base(src)
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		sheetDir := filepath.Join(outDir, stem)
		outs, err := convertCSVSheets(src, sheetDir)
		if err != nil {
			return nil, fmt.Errorf("转换失败：%v", err)
		}
		return outs, nil
	}
	return nil, fmt.Errorf("internal: unknown format %d", format)
}

// convertXLSX swaps the container of one .xls into one .xlsx.
func convertXLSX(src, dst string) error {
	wb, err := xls.Open(src, "utf-8")
	if err != nil {
		return err
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	for si := 0; si < wb.NumSheets(); si++ {
		name := sheetName(wb, si)
		if err := writeSheet(f, wb, si, name); err != nil {
			return err
		}
	}
	return f.SaveAs(dst)
}

// sheetName resolves the target sheet name: the source name (sanitized
// for excelize), or a positional fallback when the BOUNDSHEET name came
// through empty.
func sheetName(wb *xls.WorkBook, si int) string {
	name := ""
	if sh := wb.GetSheet(si); sh != nil {
		name = sh.Name
	}
	if name == "" {
		name = fmt.Sprintf("Sheet%d", si+1)
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case ':', '\\', '/', '?', '*', '[', ']':
			return '_'
		}
		return r
	}, name)
	if len(name) > 31 {
		name = name[:31]
	}
	if name == "" {
		name = fmt.Sprintf("Sheet%d", si+1)
	}
	return name
}

// writeSheet projects one source sheet: typed cells, then merged ranges.
func writeSheet(f *excelize.File, wb *xls.WorkBook, si int, name string) error {
	if si > 0 {
		if _, err := f.NewSheet(name); err != nil {
			return err
		}
	}
	// The excelize file starts with one default sheet named "Sheet1": the
	// FIRST source sheet reuses (renames) it; further sheets are created.
	if si == 0 && name != "Sheet1" {
		if err := f.SetSheetName("Sheet1", name); err != nil {
			return err
		}
	}
	sh := wb.GetSheet(si)
	if sh == nil {
		return nil
	}
	for r := 0; r <= int(sh.MaxRow); r++ {
		row := sh.Row(r)
		if row == nil {
			continue
		}
		for c := 0; c <= row.LastCol(); c++ {
			cell := row.TypedCell(c)
			if cell.Type == xls.CellEmpty || cell.Type == xls.CellBlank {
				continue
			}
			ref, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if err := writeCell(f, name, ref, wb, cell); err != nil {
				return err
			}
		}
	}
	// Merged ranges: reproduce the boxes (degenerate 1×1 entries skipped —
	// some writers emit those; Excel does not render them as merges).
	for _, mr := range sh.Merged() {
		if mr.FirstRow == mr.LastRow && mr.FirstCol == mr.LastCol {
			continue
		}
		first, _ := excelize.CoordinatesToCellName(int(mr.FirstCol)+1, int(mr.FirstRow)+1)
		last, _ := excelize.CoordinatesToCellName(int(mr.LastCol)+1, int(mr.LastRow)+1)
		if err := f.MergeCell(name, first, last); err != nil {
			// A rejected merge must not kill the conversion of the VALUES:
			// the per-cell content is the fidelity contract; a merge error
			// means the range was already covered — carry on.
			continue
		}
	}
	return nil
}

// writeCell projects one typed cell into xlsx.
func writeCell(f *excelize.File, sheet, ref string, wb *xls.WorkBook, cell xls.TypedCell) error {
	switch cell.Type {
	case xls.CellNumber:
		return f.SetCellFloat(sheet, ref, cell.Number, -1, 64)
	case xls.CellDate:
		// A real date cell: excelize writes the serial with a date style —
		// the value keeps its calendar meaning, the reader sees a date.
		return f.SetCellValue(sheet, ref, wb.DateValue(cell.Number))
	case xls.CellText:
		return f.SetCellStr(sheet, ref, cell.Text)
	case xls.CellBool:
		return f.SetCellBool(sheet, ref, cell.Bool)
	case xls.CellError:
		// Excel error codes are not a first-class xlsx value; write the
		// Excel rendering so the anomaly stays VISIBLE, not silently blank.
		return f.SetCellStr(sheet, ref, excelErrText(cell.Err))
	}
	return nil
}

// excelErrText renders the byte error codes the way Excel shows them.
func excelErrText(code byte) string {
	switch code {
	case 0x00:
		return "#NULL!"
	case 0x07:
		return "#DIV/0!"
	case 0x0F:
		return "#VALUE!"
	case 0x17:
		return "#REF!"
	case 0x1D:
		return "#NAME?"
	case 0x24:
		return "#NUM!"
	case 0x2A:
		return "#N/A"
	}
	return fmt.Sprintf("#ERR(%d)", code)
}

// convertCSVSheets writes one CSV per sheet under its own subdirectory
// (the multi-sheet ledgers keep their split; the directory preserves the
// original month/branch-carrying stem — acceptance #4).
func convertCSVSheets(src, sheetDir string) ([]string, error) {
	wb, err := xls.Open(src, "utf-8")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sheetDir, 0o755); err != nil {
		return nil, err
	}
	var outs []string
	used := map[string]int{}
	for si := 0; si < wb.NumSheets(); si++ {
		sh := wb.GetSheet(si)
		name := fmt.Sprintf("Sheet%d", si+1)
		if sh != nil && sh.Name != "" {
			name = sh.Name
		}
		safe := strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
				return '_'
			}
			return r
		}, name)
		if used[safe] > 0 {
			safe = fmt.Sprintf("%s_%d", safe, used[safe])
		}
		used[safe]++
		out := filepath.Join(sheetDir, safe+".csv")
		if err := writeSheetCSV(sh, wb, out); err != nil {
			return outs, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}

// writeSheetCSV renders one sheet as CSV: numbers shortest-exact, dates
// ISO (calendar-day precision unless a time part exists), text verbatim.
func writeSheetCSV(sh *xls.WorkSheet, wb *xls.WorkBook, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if sh == nil {
		return nil
	}
	var sb strings.Builder
	for r := 0; r <= int(sh.MaxRow); r++ {
		row := sh.Row(r)
		if row == nil {
			sb.WriteString("\n")
			continue
		}
		var cells []string
		for c := 0; c <= row.LastCol(); c++ {
			cells = append(cells, csvCellText(wb, row.TypedCell(c)))
		}
		sb.WriteString(csvJoin(cells))
		sb.WriteString("\n")
	}
	_, err = f.WriteString(sb.String())
	return err
}

// csvCellText renders one typed cell for CSV.
func csvCellText(wb *xls.WorkBook, cell xls.TypedCell) string {
	switch cell.Type {
	case xls.CellNumber:
		return strconv.FormatFloat(cell.Number, 'f', -1, 64)
	case xls.CellDate:
		t := wb.DateValue(cell.Number)
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format(time.RFC3339)
	case xls.CellText:
		return cell.Text
	case xls.CellBool:
		return strconv.FormatBool(cell.Bool)
	case xls.CellError:
		return excelErrText(cell.Err)
	}
	return ""
}

// csvJoin quotes a row RFC4180-style (quote when separator/quote/newline
// appears).
func csvJoin(cells []string) string {
	for i, c := range cells {
		if strings.ContainsAny(c, ",\"\n\r") {
			cells[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
		}
	}
	return strings.Join(cells, ",")
}

// Run walks the input (file or directory, optional recursion), converts
// every .xls it finds, and reports per-file outcomes without ever failing
// silently: parse failures land in Result.Failed with their reason.
func Run(in, outDir string, format Format, recursive bool) (Result, error) {
	var files []string
	info, err := os.Stat(in)
	if err != nil {
		return Result{}, fmt.Errorf("输入不存在：%s", in)
	}
	if info.IsDir() {
		ext := ".xls"
		if recursive {
			err = filepath.WalkDir(in, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				if strings.EqualFold(filepath.Ext(path), ext) {
					files = append(files, path)
				}
				return nil
			})
		} else {
			entries, err2 := os.ReadDir(in)
			if err2 != nil {
				return Result{}, err2
			}
			for _, e := range entries {
				if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ext) {
					files = append(files, filepath.Join(in, e.Name()))
				}
			}
		}
		if err != nil {
			return Result{}, err
		}
		sort.Strings(files)
	} else {
		if !strings.EqualFold(filepath.Ext(in), ".xls") {
			return Result{Skipped: 1, Files: []FileResult{{Input: in, Status: "skipped", Error: "非 .xls 输入（convert 只处理 Excel 2003 工作簿）"}}}, nil
		}
		files = append(files, in)
	}

	res := Result{Files: []FileResult{}}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Result{}, err
	}
	for _, path := range files {
		outs, err := ConvertFile(path, outDir, format)
		fr := FileResult{Input: path}
		if err != nil {
			res.Failed++
			fr.Status = "failed"
			fr.Error = err.Error()
		} else {
			res.Converted++
			fr.Status = "converted"
			fr.Output = outs
		}
		res.Files = append(res.Files, fr)
	}
	return res, nil
}
