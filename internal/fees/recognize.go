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

	"github.com/mmqz/netops-fees-lab/internal/workbook"
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

// SheetData is one sheet's parsed content: the sheet name ("" for the CSV
// pseudo-sheet — CSV files have no sheets) plus the header and the data
// rows in source order (#125: a real monthly ledger is ONE workbook with
// one fee type per sheet, so the sheet is a first-class coordinate of the
// import, the evidence and the idempotency key).
type SheetData struct {
	Name    string
	Headers []string
	Rows    []SourceRow
}

// ReadFeeFile streams the data rows of a CSV or xlsx fee file. The FIRST
// non-empty row is the header (multi-row headers are out of scope for the
// MVP — the real fee samples carry one header row; the recognizer refuses
// files whose header cannot be mapped, which surfaces the case loudly).
// CSV: UTF-8 with BOM tolerated; xlsx: the first sheet only (the
// backward-compatible single-sheet channel — see ReadFeeFileFirst).
func ReadFeeFile(path string) (headers []string, rows []SourceRow, err error) {
	sheet, err := ReadFeeFileFirst(path)
	if err != nil {
		return nil, nil, err
	}
	return sheet.Headers, sheet.Rows, nil
}

// ReadFeeFileFirst reads ONE sheet — the first sheet for xlsx, the file
// itself as a nameless pseudo-sheet for CSV. It is the single-sheet
// import channel's reader and keeps the pre-#125 behavior byte-for-byte
// while surfacing the sheet name (the fee-type cross-check needs it: a
// sheet named 电费 in a file declared tower_lease is a contradiction to
// refuse, not to trust).
func ReadFeeFileFirst(path string) (SheetData, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "csv":
		headers, rows, err := readCSV(path)
		if err != nil {
			return SheetData{}, err
		}
		return SheetData{Headers: headers, Rows: rows}, nil
	case "xlsx":
		return readXLSXFirst(path)
	case "xls":
		// #92: honest refusal — .xls is the pre-2007 BINARY format; excelize
		// only reads xlsx (zip+XML), so routing .xls there surfaced a
		// misleading "zip: not a valid zip file" from deep inside the
		// library instead of telling the operator what to do.
		return SheetData{}, fmt.Errorf("fees: .xls 是 Excel 2003 二进制旧格式，不受支持——请先转换后重试：netopsfees convert --in <file.xls> --out <dir>（批量保真转换，issue #7）：%s", path)
	default:
		return SheetData{}, fmt.Errorf("fees: 文件 %s 的扩展名不受支持（可用：csv / xlsx）", path)
	}
}

// ReadFeeFileSheets reads EVERY sheet of an xlsx workbook that carries
// data (a header plus at least one data row). The second return lists the
// sheets SKIPPED as cosmetic (no data rows at all — a cover page, a
// legend): nothing to import, but the caller reports them so the operator
// sees the full accounting, never a silent drop (#125). CSV answers
// exactly one nameless pseudo-sheet and an empty skip list. The sheet
// order is the workbook's own order (deterministic); each sheet's rows
// keep their 1-based sheet-local row numbers (the evidence coordinate).
// #125: the sheets the caller cannot classify are the CALLER's refusal to
// make, not this reader's to drop silently: everything with data comes
// back.
func ReadFeeFileSheets(path string) (sheets []SheetData, skipped []string, err error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "csv":
		headers, rows, err := readCSV(path)
		if err != nil {
			return nil, nil, err
		}
		return []SheetData{{Headers: headers, Rows: rows}}, nil, nil
	case "xlsx":
		return readXLSXAll(path)
	case "xls":
		return nil, nil, fmt.Errorf("fees: .xls 是 Excel 2003 二进制旧格式，不受支持——请用 Excel 另存为 .xlsx（或导出 .csv）后重试：%s", path)
	default:
		return nil, nil, fmt.Errorf("fees: 文件 %s 的扩展名不受支持（可用：csv / xlsx）", path)
	}
}

// UTF-8 BOM (EF BB BF) — Excel's CSV export prepends it; the doc contract
// says "UTF-8 with BOM tolerated", so the reader strips it before the CSV
// parser ever sees it (#98: a surviving BOM glued the first header cell
// into "\ufeff站址编码", the recognizer missed siteCode, and the whole
// file bounced out of auto-recognize with a confusing 422).
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func readCSV(path string) ([]string, []SourceRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 打开 CSV %s: %w", path, err)
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	r := csv.NewReader(bytes.NewReader(data))
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
		// #98: a row with MORE cells than the header is misaligned data —
		// the classic producer is an UNQUOTED thousands separator ("1,234.56"
		// splits into two cells, the amount cell reads "1" and the file
		// imports at 1/1000 of the truth with zero failures). The only
		// recovery hypothesis consistent with the header count is the
		// accounting grouping (1-3 digit head + exactly-3-digit tail); rows
		// that cannot reassemble fail LOUD with the row number — the
		// misparse must never reach the analysis table silently. Rows
		// SHORTER than the header keep the tolerant "" cells (a missing
		// trailing optional column stays legal).
		if len(rec) > len(headers) {
			merged, ok := reassembleThousands(rec, len(headers))
			if !ok {
				return nil, nil, fmt.Errorf("fees: CSV %s 第 %d 行有 %d 列，超出表头的 %d 列，且无法按千分位分组（1-3 位头 + 恰好 3 位尾）重组——疑似未加引号的千分位逗号；拒绝静默错位导入", path, line, len(rec), len(headers))
			}
			rec = merged
		}
		rows = append(rows, SourceRow{Number: line, Cells: rec})
	}
	if headers == nil {
		return nil, nil, fmt.Errorf("fees: CSV %s 没有可识别的表头行", path)
	}
	return headers, rows, nil
}

// reassembleThousands merges adjacent cells of split thousands-separated
// numbers until the row matches the header count (#98). Head: 1-3 pure
// ASCII digits (the leading group "1" of "1,234.56"), or the product of a
// previous merge pass (so the chain "1"+"234" → "1234" then
// "1234"+"567.89" → "1234567.89" completes — a file-native "1234" cell is
// NOT a head, keeping the false-merge surface at the single-group size).
// Tail: exactly 3 digits with an optional 1-2 digit fraction ("234.56",
// "000.00") — the accounting grouping convention is groups of EXACTLY
// three, which keeps a legit adjacent pair like ("100", "50") from merging.
// Greedy leftmost, one pair per pass, deterministic. ok=false when the
// passes cannot equalize the count (the caller fails the row loudly).
func reassembleThousands(cells []string, want int) ([]string, bool) {
	mergedHeads := map[string]bool{}
	for len(cells) > want {
		merged := false
		for i := 0; i+1 < len(cells); i++ {
			head, tail := strings.TrimSpace(cells[i]), strings.TrimSpace(cells[i+1])
			if !(isThousandsHead(head) || mergedHeads[head]) || !isThousandsTail(tail) {
				continue
			}
			out := make([]string, 0, len(cells)-1)
			out = append(out, cells[:i]...)
			out = append(out, cells[i]+cells[i+1])
			out = append(out, cells[i+2:]...)
			cells = out
			mergedHeads[head+tail] = true
			merged = true
			break
		}
		if !merged {
			return nil, false
		}
	}
	return cells, len(cells) == want
}

// isThousandsHead: the leading group — 1-3 pure ASCII digits.
func isThousandsHead(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 1 && len(s) <= 3 && allDigits(s)
}

// isThousandsTail: a continuation group — exactly 3 digits, optionally
// followed by a 1-2 digit fraction (amounts carry at most two fraction
// digits, and a third is refused downstream anyway).
func isThousandsTail(s string) bool {
	s = strings.TrimSpace(s)
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	if len(frac) > 2 {
		return false
	}
	return len(intPart) == 3 && allDigits(intPart) && (frac == "" || allDigits(frac))
}

// readXLSXFirst opens the workbook and reads the FIRST sheet (the
// pre-#125 single-sheet behavior, now also surfacing the sheet name).
// A first sheet without a recognizable header refuses here — including
// the pointer to the multi-sheet channel when the workbook carries more
// sheets (the data may live in sheet 2+; the reader will not guess).
func readXLSXFirst(path string) (SheetData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SheetData{}, fmt.Errorf("fees: 打开工作簿 %s: %w", path, err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return SheetData{}, fmt.Errorf("fees: 打开工作簿 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return SheetData{}, fmt.Errorf("fees: 工作簿 %s 没有工作表", path)
	}
	sd, err := readSheet(data, f, sheets[0])
	if err != nil {
		return SheetData{}, err
	}
	if sd.Headers == nil {
		if len(sheets) > 1 {
			return SheetData{}, fmt.Errorf("fees: 工作簿 %s 的首个工作表 %q 没有可识别的表头行（全书共 %d 个工作表）——若各 sheet 是不同费用类型，请改用多 sheet 导入模式（sheets=auto）",
				path, sheets[0], len(sheets))
		}
		return SheetData{}, fmt.Errorf("fees: 工作表 %q 没有可识别的表头行", sheets[0])
	}
	return sd, nil
}

// readXLSXAll opens the workbook and reads EVERY sheet that carries data
// (#125). Cosmetic sheets (no non-blank row at all — the header IS the
// first non-blank row, so headers == nil ⇔ nothing human-visible) come
// back in the skipped list for the caller to report. The tail check (#81)
// covers the whole workbook: a truncated zip member anywhere must not
// read as a clean prefix.
func readXLSXAll(path string) ([]SheetData, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 打开工作簿 %s: %w", path, err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("fees: 打开工作簿 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sheetNames := f.GetSheetList()
	if len(sheetNames) == 0 {
		return nil, nil, fmt.Errorf("fees: 工作簿 %s 没有工作表", path)
	}
	var out []SheetData
	var skipped []string
	for _, name := range sheetNames {
		sd, err := readSheet(data, f, name)
		if err != nil {
			return nil, nil, err
		}
		// Headers == nil ⇔ the sheet holds no non-blank row at all: a
		// cosmetic sheet — nothing to import, so nothing silently dropped;
		// a header-only sheet likewise carries zero data rows.
		if sd.Headers == nil || len(sd.Rows) == 0 {
			skipped = append(skipped, name)
			continue
		}
		out = append(out, sd)
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("fees: 工作簿 %s 的 %d 个工作表均无数据行", path, len(sheetNames))
	}
	return out, skipped, nil
}

// readSheet reads one sheet: the first non-blank row is the header, the
// rest are data rows with their 1-based sheet-local numbers. The #81
// tail check runs once per workbook (the caller passes the raw bytes).
func readSheet(data []byte, f *excelize.File, sheet string) (SheetData, error) {
	it, err := f.Rows(sheet)
	if err != nil {
		return SheetData{}, fmt.Errorf("fees: 读取工作表 %s: %w", sheet, err)
	}
	defer func() { _ = it.Close() }()

	var headers []string
	var rows []SourceRow
	rowNum := 0
	for it.Next() {
		rowNum++
		cells, err := it.Columns()
		if err != nil {
			return SheetData{}, fmt.Errorf("fees: 读取工作表 %s 第 %d 行: %w", sheet, rowNum, err)
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
		return SheetData{}, fmt.Errorf("fees: 读取工作表 %s: %w", sheet, err)
	}
	if headers == nil {
		// No header row found: distinguish "cosmetic sheet" (nothing at
		// all) from "data without a header" upstream — both sides are
		// reported, neither is silently read.
		return SheetData{Name: sheet}, nil
	}
	// #81: same honest-import guard as the resource channel — excelize's
	// iterator has no error surface for mid-stream XML corruption, so a
	// truncated sheet would otherwise import as a silent row prefix. The
	// fee channel's evidence chain (row anchors into the snapshot) is only
	// as trustworthy as the row count.
	if err := workbook.CheckWorksheetTails(data); err != nil {
		return SheetData{}, fmt.Errorf("fees: %s: %w", sheet, err)
	}
	return SheetData{Name: sheet, Headers: headers, Rows: rows}, nil
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

// sheetNameMarkers scores the SHEET NAME against each fee type (#125): a
// real monthly ledger names its sheets exactly by fee type (电费 /
// 铁塔租费 / 修理费), so the sheet name is the operator's own type
// declaration sitting in the file. Same contains-match folding as the
// header markers (headerMatchKey — full-width spaces removed,
// case-folded), covering the English exports too.
var sheetNameMarkers = map[FeeType][]string{
	FeeTypeElectricity: {"电费", "用电", "电量", "电价", "electricity", "electric"},
	FeeTypeTowerLease:  {"铁塔", "塔租", "塔费", "tower", "lease"},
	FeeTypeRepair:      {"修理", "维修", "抢修", "repair"},
	FeeTypeMaintenance: {"综维", "维护", "驻场", "代维", "巡检", "maintenance"},
}

// scoredFeeTypes is feeTypeMarkers' key set in sorted name order — every
// scoring loop must iterate THIS slice, never the map directly (Go map
// iteration order is random: a tie broken by "first iterated wins" would
// make the recognizer nondeterministic, violating the project-wide ACC-4
// determinism doctrine; the pre-#125 Recognize had exactly this latent
// defect on tie scores and #125's classifier inherits the fixed order).
var scoredFeeTypes = []FeeType{FeeTypeElectricity, FeeTypeMaintenance, FeeTypeRepair, FeeTypeTowerLease}

// RecognizeSheetType types one sheet from its name + headers (#125).
// Score = 2×(sheet-name marker hits) + 1×(header marker hits) — the
// sheet name is the operator's own naming and outweighs any single
// header coincidence. The winner must be UNIQUE (strictly above every
// other type) and score ≥ 2, else the answer is "" (unclassified: an
// ambiguous or silent sheet is the caller's explicit refusal, never a
// guess). Deterministic: types visited in scoredFeeTypes order, markers
// in declaration order.
func RecognizeSheetType(sheetName string, headers []string) (FeeType, int) {
	nameKey := headerMatchKey(sheetName)
	scores := make(map[FeeType]int, len(scoredFeeTypes))
	for _, typ := range scoredFeeTypes {
		score := 0
		for _, m := range sheetNameMarkers[typ] {
			if strings.Contains(nameKey, headerMatchKey(m)) {
				score += 2 // the sheet name is a first-class declaration
				break      // one hit per type suffices — the name is one string
			}
		}
		for _, m := range feeTypeMarkers[typ] {
			mk := headerMatchKey(m)
			for _, h := range headers {
				if strings.Contains(headerMatchKey(h), mk) {
					score++
					break // one hit per marker
				}
			}
		}
		scores[typ] = score
	}
	best, bestScore := FeeType(""), 0
	for _, typ := range scoredFeeTypes {
		if scores[typ] > bestScore {
			best, bestScore = typ, scores[typ]
		}
	}
	if bestScore < 2 {
		return "", bestScore // silence beats a fuzzy guess
	}
	// Uniqueness: a tie means ambiguity — refuse to pick a side.
	for _, typ := range scoredFeeTypes {
		if typ != best && scores[typ] == bestScore {
			return "", bestScore
		}
	}
	return best, bestScore
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

// ledgerFieldSynonyms maps the OPTIONAL ledger richness columns of issue
// #5 to their header synonyms (trimmed/normalized keys — the same
// headerMatchKey discipline as coreFieldSynonyms; contains-match fallback
// catches the unit-suffixed variants like 上期读数(度)). Every field is
// optional: no hit = the column stays nil and the import is byte-identical
// to the pre-#5 behavior (acceptance #2).
var ledgerFieldSynonyms = map[string][]string{
	// recordKey (#4): the business-document identity — tower-lease order
	// numbers, electricity meter numbers (the meter IS the electricity
	// record identity, so 电表编号 maps BOTH here and to meterNo), repair /
	// maintenance work orders and settlement sheet numbers.
	"recordKey":    {"订单号", "需求确认单编号", "确认单编号", "工单号", "结算单号", "电表编号", "表号"},
	"meterNo":      {"电表编号", "表号", "电表号"},
	"readingStart": {"起始读数", "上期读数", "起始示数", "上期示数"},
	"readingEnd":   {"截止读数", "本期读数", "截止示数", "本期示数", "终止读数", "止数"},
	"meterPrevEnd": {"旧表止数", "旧表读数"},
	"meterSwapped": {"是否换表", "换表标记"},
	"quantity":     {"实际电量", "用电量", "电量"},
	"unitPrice":    {"单价", "电价"},
	"shareMobile":  {"移动分摊比例", "移动分摊"},
	"shareUnicom":  {"联通分摊比例", "联通分摊"},
	"shareTelecom": {"电信分摊比例", "电信分摊"},
	"shareTower":   {"铁塔分摊比例", "铁塔分摊"},
}

// LedgerFieldKeys lists the optional ledger field keys (canonical order).
func LedgerFieldKeys() []string {
	return []string{
		"recordKey", "meterNo", "readingStart", "readingEnd", "meterPrevEnd", "meterSwapped",
		"quantity", "unitPrice",
		"shareMobile", "shareUnicom", "shareTelecom", "shareTower",
	}
}

// RecognizeLedgerFields resolves the optional ledger columns from the
// headers (same match discipline as the core walk: exact key match first,
// contains fallback for unit-suffixed variants). Returns target->header
// with only the hits — a key absent from the map means the file carries
// no such column.
func RecognizeLedgerFields(headers []string) map[string]string {
	keys := make([]string, len(headers))
	for i, h := range headers {
		keys[i] = headerMatchKey(h)
	}
	cols := make(map[string]string)
	for field, syns := range ledgerFieldSynonyms {
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
	return cols
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
	// fragments of real headers). Iteration runs over scoredFeeTypes (the
	// sorted key slice), NOT the map — a tie broken by random map order
	// would make the suggestion nondeterministic (ACC-4; #125 fixed the
	// latent defect while adding the sheet classifier).
	best := FeeType("")
	bestScore := 0
	for _, typ := range scoredFeeTypes {
		markers := feeTypeMarkers[typ]
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
