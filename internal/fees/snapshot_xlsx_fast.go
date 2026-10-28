package fees

// snapshot_xlsx_fast.go — the #39 latency fix of the xlsx evidence
// read-back: a byte-scanning two-row reader.
//
// The problem it solves (measured on the issue's own shape, 157 columns x
// 454 rows): excelize's Rows() iterator must xml.Token()-walk through
// EVERY cell of EVERY row between the header and the target — ~300k
// decoder tokens ≈ 200-230ms on a 2-core VM, over the #39 acceptance
// budget (< 200ms) before the render even starts. The evidence contract
// only needs TWO rows' strings: the header (the first non-blank row) and
// the requested row. So this reader:
//
//   - inflates the ONE first-sheet XML part and byte-scans its <row>
//     boundaries (bytes.Index over the raw sheet text — no token decode),
//   - xml-decodes ONLY the needed row segments (the header candidates up
//     to the first that renders non-blank, plus the target),
//   - byte-scans sharedStrings.xml counting <si> entries and decodes only
//     the (at most two-rows'-worth of) cited ones,
//   - renders the cells with EXACTLY excelize's getValueFrom semantics
//     for the cases where identity is PROVEN (source reading + the
//     snapshot_xlsx_fast_test.go experiment: S==0 always; S>0 iff the
//     cited cellXfs numFmtId is 0/absent, for the string and number
//     families), and
//   - BAILS to the original excelize walk (readSnapshotRowXLSXExcelize)
//     for every case it cannot prove: non-General number formats (date-
//     styled cells render through excelize's full numfmt machinery),
//     styled booleans, the exotic types (t="d"/t="e"), structural
//     surprises, oversized parts. Styled sheets pay the old cost; the
//     contract never shifts — the fast path only ever answers what the
//     slow path would answer.
//
// Fidelity rulings mirrored from excelize v2.11.0 (cell.go getValueFrom,
// rows.go rowXMLHandler), each pinned by TestFastXLSXRowReaderEquivalence:
//
//   - t="s"        sharedStrings[idx] verbatim;
//   - t="str"      the <v> text as-is (a formula's cached string result —
//                  getValueFrom returns it WITHOUT formattedValue);
//   - t="inlineStr" the <is> <t> runs concatenated in document order;
//   - t="b"        "1" -> TRUE, "0" -> FALSE, anything else -> <v> as-is;
//   - no t         isNumeric normalization: ParseFloat then FormatFloat
//                  ('f', -1) — "0123.00" as a NUMBER renders "123" — then
//                  verbatim; strings are never numeric-normalized;
//   - a cell whose rendered value is empty extends NOTHING (rowXMLHandler
//                  `val != "" || F != nil`): the row's Cells slice ends at
//                  the last non-empty (or formula) column — the trailing-
//                  empty truncation the excelize walk produces;
//   - S beyond cellXfs range renders verbatim (formattedValue's bounds
//                  guard) — safe on the fast path too.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// errFastPathBail hands control back to the excelize walk: every case the
// fast reader cannot render provably identically. The caller sees
// (handled=false, nil error) and re-runs the streaming implementation.
var errFastPathBail = errors.New("fees: xlsx fast path bail")

// maxFastSheetBytes caps the in-memory sheet part (the excelize path
// streams; the fast path holds the part to byte-scan it). 96MB is ~2.5M
// rows of this repo's own wide-sheet shape.
const maxFastSheetBytes = 96 << 20

// rawCell mirrors the <c> children the renderer needs.
type rawCell struct {
	R  string `xml:"r,attr"`
	S  int    `xml:"s,attr"`
	T  string `xml:"t,attr"`
	V  string `xml:"v"`
	F  *rawF  `xml:"f"`
	IS *rawIS `xml:"is"`
}

// rawF only needs to EXIST: a formula cell extends the rendered row even
// when its cached value is empty (the rowXMLHandler guard).
type rawF struct {
	Content string `xml:",chardata"`
}

// rawIS is the inline-string container: plain <is><t>x</t></is> or the
// rich-text runs <is><r><t>a</t></r><r><t>b</t></r></is> — excelize
// concatenates every <t> in document order, so does the renderer.
type rawIS struct {
	AllT []string  `xml:"t"`
	R    []rawRuns `xml:"r"`
}

type rawRuns struct {
	T []string `xml:"t"`
}

type rawRow struct {
	R     int       `xml:"r,attr"`
	Cells []rawCell `xml:"c"`
}

// rowWrap is the decoding shell of one byte-scanned row segment.
type rowWrap struct {
	Row rawRow `xml:"row"`
}

// readSnapshotRowXLSXFast answers (RowEvidence, handled, error). handled
// false with a nil error means BAIL: re-run the excelize walk.
func readSnapshotRowXLSXFast(path string, row int) (RowEvidence, bool, error) {
	if row < 1 {
		return RowEvidence{}, false, fmt.Errorf("fees: 行号必须 ≥ 1（源文件自身编号，表头 = 1）")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return RowEvidence{}, false, fmt.Errorf("fees: 打开快照 %s: %w", path, err)
	}
	defer func() { _ = zr.Close() }()

	sheetName, err := firstSheetPart(&zr.Reader)
	if err != nil {
		return RowEvidence{}, false, err
	}
	part := zipPart(&zr.Reader, sheetName)
	if part == nil {
		return RowEvidence{}, false, nil // let the walk speak its own error
	}
	if part.UncompressedSize64 > maxFastSheetBytes {
		return RowEvidence{}, false, nil
	}
	rc, err := part.Open()
	if err != nil {
		return RowEvidence{}, false, nil // bail: the walk retries the same open
	}
	sheetXML, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return RowEvidence{}, false, nil
	}

	segs, targetIdx, lastRowNo, err := scanSheetRows(sheetXML, row)
	if err != nil {
		return RowEvidence{}, false, err
	}
	if targetIdx < 0 {
		// the same contract as the walk: a row past the end answers the
		// typed not-found with the row count the scan saw.
		return RowEvidence{}, true, fmt.Errorf("%w: fees: 快照 %s 共 %d 行，第 %d 行不存在",
			domain.ErrNotFound, baseName(path), lastRowNo, row)
	}

	// The sst cache is shared by the header candidates and the target —
	// cited indices resolve once.
	cache := map[int]string{}
	render := func(seg []byte) ([]string, error) {
		return renderRowSegment(&zr.Reader, seg, cache)
	}

	var ev RowEvidence
	for i := 0; i <= targetIdx; i++ {
		if !segs[i].hasValue {
			continue // no <v>/<is>/<f> — nothing can render
		}
		cells, rerr := render(segs[i].seg)
		if rerr != nil {
			return RowEvidence{}, false, rerr
		}
		if !isBlankRow(cells) {
			ev.Header = trimAll(cells)
			break
		}
	}
	cells, rerr := render(segs[targetIdx].seg)
	if rerr != nil {
		return RowEvidence{}, false, rerr
	}
	ev.Cells = cells
	return ev, true, nil
}

// sheetRowSeg is one byte-scanned row: its slice inside the sheet XML and
// the conservative has-value peek.
type sheetRowSeg struct {
	seg       []byte
	hasValue  bool
	rowNumber int
}

// scanSheetRows byte-walks the <row> boundaries of sheetData (no token
// decode) and answers the segments up to and including the target row,
// the target's index among them, and the highest row number seen.
func scanSheetRows(sheetXML []byte, row int) ([]sheetRowSeg, int, int, error) {
	dataStart := bytes.Index(sheetXML, []byte("<sheetData"))
	if dataStart < 0 {
		return nil, -1, 0, errFastPathBail // no sheetData — the walk owns it
	}
	var (
		segs      []sheetRowSeg
		targetIdx = -1
		lastNo    int
		seqNo     int
		from      = dataStart
	)
	// The sheetData close sits AFTER the last row — locating it once keeps
	// the per-row scan linear (searching it per iteration would re-scan
	// the whole tail for every row: the 454-row shape at ~4MB/scan was
	// the 700ms pathology).
	sheetClose := bytes.Index(sheetXML, []byte("</sheetData>"))
	if sheetClose < 0 {
		sheetClose = len(sheetXML)
	}
	for targetIdx < 0 {
		i := nextRowStart(sheetXML, from)
		if i < 0 {
			break
		}
		end := nextRowStart(sheetXML, i+4)
		if end < 0 || end > sheetClose {
			end = sheetClose
		}
		if end <= i {
			end = len(sheetXML)
		}
		tagEnd := bytes.IndexByte(sheetXML[i:end], '>')
		if tagEnd < 0 {
			return nil, -1, 0, errFastPathBail
		}
		no, ok := rowNoFromTag(sheetXML[i : i+tagEnd+1])
		if !ok {
			seqNo++
			no = seqNo
		} else if no > seqNo {
			seqNo = no
		}
		if no > lastNo {
			lastNo = no
		}
		seg := sheetXML[i:end]
		hasValue := bytes.Contains(seg, []byte("<v")) ||
			bytes.Contains(seg, []byte("<is")) ||
			bytes.Contains(seg, []byte("<f"))
		if no == row {
			targetIdx = len(segs)
		}
		segs = append(segs, sheetRowSeg{seg: seg, hasValue: hasValue, rowNumber: no})
		if no >= row {
			break // ascending order — the target cannot appear later
		}
		from = end
	}
	return segs, targetIdx, lastNo, nil
}

// nextRowStart finds the next "<row" that begins an actual row element
// (followed by a delimiter, so <rowCacheField-style names never match).
func nextRowStart(b []byte, from int) int {
	for {
		i := bytes.Index(b[from:], []byte("<row"))
		if i < 0 {
			return -1
		}
		i += from
		if i+4 >= len(b) {
			return -1
		}
		if c := b[i+4]; c == ' ' || c == '>' || c == '/' || c == '\t' || c == '\r' || c == '\n' {
			return i
		}
		from = i + 4
	}
}

// rowNoFromTag reads the r="N" attribute off a "<row ...>" start tag.
func rowNoFromTag(tag []byte) (int, bool) {
	i := bytes.Index(tag, []byte(`r="`))
	if i < 0 {
		return 0, false
	}
	rest := tag[i+3:]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(rest[:end])))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// renderRowSegment decodes one row segment and renders its cells with the
// proven-identity excelize semantics; any doubt answers errFastPathBail.
func renderRowSegment(zr *zip.Reader, seg []byte, sstCache map[int]string) ([]string, error) {
	var wrap rowWrap
	if err := xml.Unmarshal([]byte("<w>"+string(seg)+"</w>"), &wrap); err != nil {
		return nil, errFastPathBail
	}
	cells := wrap.Row.Cells

	// Pass 1: the bail checks + the sst citations.
	var needs []int
	styled := false
	for i := range cells {
		c := &cells[i]
		switch c.T {
		case "", "s", "str", "inlineStr", "b":
		default:
			return nil, errFastPathBail // t="d"/t="e"/exotic — the walk's machinery owns it
		}
		if c.S > 0 {
			styled = true
			if c.T == "b" {
				return nil, errFastPathBail // styled bools render through format() — unproven
			}
		}
		if c.T == "s" && c.V != "" {
			idx, err := strconv.Atoi(strings.TrimSpace(c.V))
			if err != nil {
				return nil, errFastPathBail // excelize answers an error here; let it
			}
			if _, cached := sstCache[idx]; !cached {
				needs = append(needs, idx)
			}
		}
	}
	if styled && !stylesProvableGeneral(zr) {
		return nil, errFastPathBail
	}
	if len(needs) > 0 {
		if err := resolveSharedStrings(zr, needs, sstCache); err != nil {
			return nil, err
		}
	}

	// Pass 2: render with the trailing-empty truncation semantics.
	vals := make(map[int]string)
	last := -1
	seq := -1
	for i := range cells {
		c := &cells[i]
		seq++
		col := seq
		if c.R != "" {
			idx, err := colIndexFromRef(c.R)
			if err != nil {
				return nil, errFastPathBail
			}
			col = idx
		}
		val, err := renderRawCell(c, sstCache)
		if err != nil {
			return nil, err
		}
		if val != "" || c.F != nil {
			vals[col] = val
			if col > last {
				last = col
			}
		}
	}
	if last < 0 {
		return []string{}, nil
	}
	out := make([]string, last+1)
	for i := range out {
		out[i] = vals[i] // absent = "" — the appendSpace padding
	}
	return out, nil
}

// renderRawCell applies the proven excelize rendering of one cell.
func renderRawCell(c *rawCell, sstCache map[int]string) (string, error) {
	switch c.T {
	case "s":
		if c.V == "" {
			return "", nil
		}
		idx, err := strconv.Atoi(strings.TrimSpace(c.V))
		if err != nil {
			return "", errFastPathBail
		}
		return sstCache[idx], nil
	case "str":
		return c.V, nil
	case "inlineStr":
		if c.IS == nil {
			return "", nil
		}
		var sb strings.Builder
		for _, t := range c.IS.AllT {
			sb.WriteString(t)
		}
		for _, r := range c.IS.R {
			for _, t := range r.T {
				sb.WriteString(t)
			}
		}
		return sb.String(), nil
	case "b":
		if c.V == "1" {
			return "TRUE", nil
		}
		if c.V == "0" {
			return "FALSE", nil
		}
		return c.V, nil
	default: // the number family: isNumeric normalization, then verbatim
		return normalizeNumericExcelize(c.V), nil
	}
}

// normalizeNumericExcelize mirrors excelize's isNumeric + FormatFloat
// branch: "0123.00" (a NUMBER cell) renders "123"; a non-numeric or
// underscore-carrying text stays as-is.
func normalizeNumericExcelize(v string) string {
	if strings.Contains(v, "_") {
		return v
	}
	flt, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	noSci := strconv.FormatFloat(flt, 'f', -1, 64)
	precision := len(noSci) - strings.Count(noSci, ".")
	if precision > 15 {
		return strconv.FormatFloat(flt, 'G', 15, 64)
	}
	return noSci
}

// colIndexFromRef converts "A454" to the 0-based column index.
func colIndexFromRef(ref string) (int, error) {
	col := 0
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		if ch >= 'A' && ch <= 'Z' {
			col = col*26 + int(ch-'A') + 1
			continue
		}
		if ch >= 'a' && ch <= 'z' {
			col = col*26 + int(ch-'a') + 1
			continue
		}
		if col == 0 {
			return 0, fmt.Errorf("fees: 非法单元格引用 %q", ref)
		}
		return col - 1, nil
	}
	if col == 0 {
		return 0, fmt.Errorf("fees: 非法单元格引用 %q", ref)
	}
	return col - 1, nil
}

// stylesProvableGeneral reports whether every cellXfs entry is General-
// formatted (numFmtId 0 or absent) — the condition under which styled
// cells render verbatim (the equivalence experiment). Out-of-range S
// indexes never reach here: renderRowSegment only calls this when some
// cell cites S>0, and excelize's bounds guard renders those verbatim.
func stylesProvableGeneral(zr *zip.Reader) bool {
	part := zipPart(zr, "xl/styles.xml")
	if part == nil {
		return false // S cited but no styles — let the walk answer its own error
	}
	rc, err := part.Open()
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()
	var st struct {
		CellXfs struct {
			Xf []struct {
				NumFmtID *int `xml:"numFmtId,attr"`
			} `xml:"xf"`
		} `xml:"cellXfs"`
	}
	if err := xml.NewDecoder(rc).Decode(&st); err != nil {
		return false
	}
	for _, xf := range st.CellXfs.Xf {
		if xf.NumFmtID != nil && *xf.NumFmtID != 0 {
			return false
		}
	}
	return true
}

// resolveSharedStrings byte-scans sharedStrings.xml counting <si> entries
// and decodes only the cited ones into the cache.
func resolveSharedStrings(zr *zip.Reader, needs []int, cache map[int]string) error {
	wanted := make(map[int]bool, len(needs))
	for _, n := range needs {
		wanted[n] = true
	}
	part := zipPart(zr, "xl/sharedStrings.xml")
	if part == nil {
		// t="s" cells in a file without an sst — the walk answers its own
		// error; do not invent one here.
		return errFastPathBail
	}
	rc, err := part.Open()
	if err != nil {
		return errFastPathBail
	}
	defer func() { _ = rc.Close() }()
	sst, err := io.ReadAll(rc)
	if err != nil {
		return errFastPathBail
	}
	idx := -1
	from := 0
	for len(wanted) > 0 {
		i := nextSiStart(sst, from)
		if i < 0 {
			break
		}
		end := nextSiStart(sst, i+3)
		if end < 0 {
			end = len(sst)
		}
		closeAt := bytes.LastIndex(sst[i:end], []byte("</si>"))
		if closeAt >= 0 {
			end = i + closeAt + len("</si>")
		}
		idx++
		if wanted[idx] {
			var is rawIS
			if err := xml.Unmarshal([]byte("<is>"+string(sst[i+4:end-len("</si>")])+"</is>"), &is); err != nil {
				return errFastPathBail
			}
			var sb strings.Builder
			for _, t := range is.AllT {
				sb.WriteString(t)
			}
			for _, r := range is.R {
				for _, t := range r.T {
					sb.WriteString(t)
				}
			}
			cache[idx] = sb.String()
			delete(wanted, idx)
		}
		from = end
	}
	if len(wanted) > 0 {
		// citations past the sst end — excelize answers invalid index;
		// hand the case back rather than inventing "" (presence vs absence).
		return errFastPathBail
	}
	return nil
}

// nextSiStart finds the next <si> element start (attribute-less in
// practice; "<si " tolerated).
func nextSiStart(b []byte, from int) int {
	for {
		i := bytes.Index(b[from:], []byte("<si"))
		if i < 0 {
			return -1
		}
		i += from
		if i+3 >= len(b) {
			return -1
		}
		if c := b[i+3]; c == '>' || c == ' ' {
			return i
		}
		from = i + 3
	}
}

// firstSheetPart resolves the workbook's FIRST sheet part path via
// workbook.xml + its rels (GetSheetList order — the same sheet the
// excelize walk reads as sheets[0]).
func firstSheetPart(zr *zip.Reader) (string, error) {
	rc, err := openZipPart(zr, "xl/workbook.xml")
	if err != nil {
		return "", errFastPathBail
	}
	var wb struct {
		Sheets struct {
			Sheet []struct {
				RID string `xml:"id,attr"`
			} `xml:"sheet"`
		} `xml:"sheets"`
	}
	derr := xml.NewDecoder(rc).Decode(&wb)
	_ = rc.Close()
	if derr != nil || len(wb.Sheets.Sheet) == 0 {
		return "", errFastPathBail
	}
	rid := wb.Sheets.Sheet[0].RID
	if rid == "" {
		return "", errFastPathBail
	}
	rc2, err := openZipPart(zr, "xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", errFastPathBail
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	rerr := xml.NewDecoder(rc2).Decode(&rels)
	_ = rc2.Close()
	if rerr != nil {
		return "", errFastPathBail
	}
	for _, r := range rels.Rel {
		if r.ID == rid {
			target := strings.TrimPrefix(r.Target, "/")
			if !strings.HasPrefix(target, "xl/") {
				target = "xl/" + strings.TrimPrefix(target, "./")
			}
			return target, nil
		}
	}
	return "", errFastPathBail
}

func openZipPart(zr *zip.Reader, name string) (io.ReadCloser, error) {
	part := zipPart(zr, name)
	if part == nil {
		return nil, fmt.Errorf("fees: xlsx 缺少 %s", name)
	}
	return part.Open()
}

func zipPart(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// baseName is filepath.Base without the import (this file keeps its deps
// to the stdlib archive/zip + encoding/xml surface).
func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	if i := strings.LastIndexByte(path, '\\'); i >= 0 {
		return path[i+1:]
	}
	return path
}
