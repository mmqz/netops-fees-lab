package fees

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/mmqz/netops-lab/internal/workbook"
	"github.com/xuri/excelize/v2"
)

// The FILE READER + HEADER RECOGNITION layer (#61): one row stream for CSV
// and xlsx alike (row numbers are 1-based data-row numbers matching the
// source file's own numbering — the evidence coordinates), plus the
// fee-type / core-column recognizer (header synonyms of the four fee
// domains, W3's SuggestType precedent applied to the fee world).

// SourceRow is one physical data row of a fee file: its 1-based row number
// (the source file's own numbering — Excel rows are 2+ after the header;
// CSV likewise) and the raw cells in header order. Raw values are verbatim
// (never trimmed here — normalization is an explicit, tested step).
type SourceRow struct {
	Number int
	Cells  []string
}

// ReadFeeFile streams the data rows of a CSV or xlsx fee file. The FIRST
// non-empty row is the header (multi-row headers are out of scope for the
// MVP — the real fee samples carry one header row; the recognizer refuses
// files whose header cannot be mapped, which surfaces the case loudly).
// CSV: UTF-8 with BOM tolerated; xlsx: the first sheet only.
func ReadFeeFile(path string) (headers []string, rows []SourceRow, err error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "csv":
		return readCSV(path)
	case "xlsx":
		return readXLSX(path)
	case "xls":
		// #92: honest refusal — .xls is the pre-2007 BINARY format; excelize
		// only reads xlsx (zip+XML), so routing .xls there surfaced a
		// misleading "zip: not a valid zip file" from deep inside the
		// library instead of telling the operator what to do.
		return nil, nil, fmt.Errorf("fees: .xls 是 Excel 2003 二进制旧格式，不受支持——请用 Excel 另存为 .xlsx（或导出 .csv）后重试：%s", path)
	default:
		return nil, nil, fmt.Errorf("fees: 文件 %s 的扩展名不受支持（可用：csv / xlsx）", path)
	}
}

func readCSV(path string) ([]string, []SourceRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 打开 CSV %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1 // ragged rows tolerated; mapping is by header
	r.TrimLeadingSpace = true

	var headers []string
	var rows []SourceRow
	line := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("fees: 读取 CSV %s 第 %d 行: %w", path, line+1, err)
		}
		line++
		if isBlankRow(rec) {
			continue
		}
		if headers == nil {
			headers = trimAll(rec)
			continue
		}
		rows = append(rows, SourceRow{Number: line, Cells: rec})
	}
	if headers == nil {
		return nil, nil, fmt.Errorf("fees: CSV %s 没有可识别的表头行", path)
	}
	return headers, rows, nil
}

func readXLSX(path string) ([]string, []SourceRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 打开工作簿 %s: %w", path, err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 打开工作簿 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil, fmt.Errorf("fees: 工作簿 %s 没有工作表", path)
	}
	sheet := sheets[0]
	it, err := f.Rows(sheet)
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 读取工作表 %s: %w", sheet, err)
	}
	defer func() { _ = it.Close() }()

	var headers []string
	var rows []SourceRow
	rowNum := 0
	for it.Next() {
		rowNum++
		cells, err := it.Columns()
		if err != nil {
			return nil, nil, fmt.Errorf("fees: 读取工作表 %s 第 %d 行: %w", sheet, rowNum, err)
		}
		strs := make([]string, len(cells))
		for i, c := range cells {
			strs[i] = c
		}
		if isBlankRow(strs) {
			continue
		}
		if headers == nil {
			headers = trimAll(strs)
			continue
		}
		rows = append(rows, SourceRow{Number: rowNum, Cells: strs})
	}
	// #81: it.Next() returns false on BOTH clean end and iteration error
	// (excelize discards xml.Decoder errors); it.Error() is the only error
	// surface the iterator exposes — not checking it reads corruption as
	// a clean end-of-file.
	if err := it.Error(); err != nil {
		return nil, nil, fmt.Errorf("fees: 读取工作表 %s: %w", sheet, err)
	}
	if headers == nil {
		return nil, nil, fmt.Errorf("fees: 工作表 %s 没有可识别的表头行", sheet)
	}
	// #81: same honest-import guard as the resource channel — excelize's
	// iterator has no error surface for mid-stream XML corruption, so a
	// truncated sheet would otherwise import as a silent row prefix. The
	// fee channel's evidence chain (row anchors into the snapshot) is only
	// as trustworthy as the row count.
	if err := workbook.CheckWorksheetTails(data); err != nil {
		return nil, nil, fmt.Errorf("fees: %s: %w", path, err)
	}
	return headers, rows, nil
}

func isBlankRow(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func trimAll(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.TrimSpace(c)
	}
	return out
}

// ---- header recognition ----

// coreFieldSynonyms maps the canonical core target fields to the header
// synonyms real fee files use. The recognizer scores every synonym hit;
// coreFieldSynonyms maps the canonical core target fields to the header
// synonyms real fee files use. The recognizer scores every synonym hit;
// the suggested mapping is the best synonym per field. Chinese synonyms
// stay first in list order (files with Chinese headers keep the exact
// suggestions they always had); the English tail covers the other-system
// exports (#65 P3-d: site_code / Site Code / SITE_CODE / station_code /
// cell_code / amount / period … all auto-recognize now).
var coreFieldSynonyms = map[string][]string{
	"siteCode": {"站址编码", "站址编号", "站址ID", "站点编码", "站点编号", "站点代码", "资源编码", "资源ID", "机房编码", "基站编码", "局站编码", "局站ID", "sitecode", "siteid", "stationcode", "cellcode"},
	"amount":   {"电费", "铁塔租费", "塔租费", "租费", "修理费", "维修费", "综维费", "费用金额", "金额", "应付金额", "实付金额", "含税金额", "费用", "amount"},
	"period":   {"账期", "所属账期", "所属期间", "费用期间", "账单期间", "月份", "所属月份", "计费周期", "账务期间", "年月", "period", "billingperiod", "month"},
	"date":     {"日期", "计费日期", "账单日期", "结算日期", "费用日期", "发生日期", "扣费日期", "date", "billingdate"},
}

// feeTypeMarkers scores a header set against each fee type: type-specific
// markers (电量/供电主体 → electricity, 塔类/起租日 → tower_lease,
// 工单/故障 → repair, 综维/驻场 → maintenance). The recognizer only
// HINTS — the operator's --type declaration always wins.
var feeTypeMarkers = map[FeeType][]string{
	FeeTypeElectricity: {"电量", "供电", "用电", "电价", "功率因数", "电表"},
	FeeTypeTowerLease:  {"塔租", "铁塔", "塔类", "起租", "塔桅", "挂高"},
	FeeTypeRepair:      {"工单", "故障", "维修", "抢修", "材料费", "修理"},
	FeeTypeMaintenance: {"综维", "驻场", "巡检", "代维", "维护费", "人员"},
}

// headerMatchKey folds a header (or synonym) into its comparison key:
// lower-cased, with spaces, underscores, hyphens and full-width spaces
// removed — "Site Code" / "site_code" / "SITE-CODE" / "sitecode" share
// one key. Chinese text is untouched (lower-casing is a no-op), so every
// Chinese-only header decision is identical to the previous exact
// matcher (#65 P3-d added English/ mixed-form headers; it changed
// nothing for the forms that already matched).
func headerMatchKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '_', '-', '　':
			continue
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Recognize inspects the headers and returns (suggested fee type, "" when
// no marker hits; suggested core column mapping target→source header, ""
// for fields without a hit). Pure scoring on trimmed headers — no
// mutation, no I/O; deterministic (first synonym wins ties in header
// order, synonym list order).
func Recognize(headers []string) (FeeType, map[string]string) {
	keys := make([]string, len(headers))
	for i, h := range headers {
		keys[i] = headerMatchKey(h)
	}
	// core fields: key match first, then a contains fallback —
	// real headers carry unit suffixes (电费(元), 金额(含税), amount(Yuan)…),
	// and the synonym is the semantic core of the header, not its full text.
	cols := make(map[string]string, 4)
	for field, syns := range coreFieldSynonyms {
		for _, syn := range syns {
			k := headerMatchKey(syn)
			for i, h := range headers {
				if keys[i] == k {
					cols[field] = h
					break
				}
			}
			if cols[field] != "" {
				break
			}
		}
		if cols[field] == "" {
			for _, syn := range syns {
				k := headerMatchKey(syn)
				for i, h := range headers {
					if strings.Contains(keys[i], k) {
						cols[field] = h
						break
					}
				}
				if cols[field] != "" {
					break
				}
			}
		}
	}
	// fee type: count marker hits (contains-match — markers are word
	// fragments of real headers)
	best := FeeType("")
	bestScore := 0
	for typ, markers := range feeTypeMarkers {
		score := 0
		for _, m := range markers {
			mk := headerMatchKey(m)
			for _, h := range headers {
				if strings.Contains(headerMatchKey(h), mk) {
					score++
					break
				}
			}
		}
		if score > bestScore {
			best, bestScore = typ, score
		}
	}
	return best, cols
}

// cellAt returns the trimmed cell of row at the position of header target
// (the mapping resolves header → index once per file; the caller passes
// the resolved index).
func cellAt(row SourceRow, idx int) string {
	if idx < 0 || idx >= len(row.Cells) {
		return ""
	}
	return strings.TrimSpace(row.Cells[idx])
}

// headerIndex finds the exact header position (-1 when absent) — the
// mapping's source values must exist verbatim in the header (a mapping
// naming a column that is not in the file is a caller error, not a
// silent skip).
func headerIndex(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}

// AtoiOr is strconv.Atoi with a default (year/month flags).
func AtoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}
