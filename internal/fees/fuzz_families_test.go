package fees

// fuzz_families_test.go — the #33 companion pin tables: every fuzz target
// asserts INVARIANTS over arbitrary inputs, but the ordinary `go test` run
// only executes the SEED corpus — these table tests pin the same surfaces'
// KNOWN shapes so a regression fails in one named test with a readable
// message instead of depending on what the nightly engine happens to
// find. One family per behavior, no shared helpers with the fuzz file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// ---- headerMatchKey families ----

// TestHeaderMatchKeyFamiliesTable pins the fold contract: the separator
// families share ONE key, and Chinese text passes through untouched (the
// #65 P3-d doctrine — folding changed nothing for the forms that already
// matched).
func TestHeaderMatchKeyFamiliesTable(t *testing.T) {
	same := [][2]string{
		{"Site Code", "sitecode"},
		{"site_code", "sitecode"},
		{"SITE-CODE", "sitecode"},
		{"站 址 编 码", "站址编码"},
		{"站_址_编_码", "站址编码"},
		{"站-址-编-码", "站址编码"},
		{"站\t址\t编\t码", "站址编码"},
		{"站\n址\r\n编\n码", "站址编码"},
		{"产品服务费合计\n（出账费用）（不含税）", "产品服务费合计（出账费用）（不含税）"},
	}
	for _, c := range same {
		if headerMatchKey(c[0]) != headerMatchKey(c[1]) {
			t.Errorf("%q and %q must share one key: %q vs %q", c[0], c[1], headerMatchKey(c[0]), headerMatchKey(c[1]))
		}
	}
	if headerMatchKey("站址编码") != "站址编码" {
		t.Fatalf("Chinese-only header must pass through untouched: %q", headerMatchKey("站址编码"))
	}
}

// TestHeaderMatchKeyUnicodeSpacesFold pins the #33 widening: the whole
// unicode space family folds — NBSP and the Zs separators that the
// pre-#33 hand-list missed.
func TestHeaderMatchKeyUnicodeSpacesFold(t *testing.T) {
	for _, ws := range []string{"\u00a0", "\u2000", "\u2001", "\u2002", "\u2003", "\u3000", "\v", "\f"} {
		if headerMatchKey("金"+ws+"额") != "金额" {
			t.Errorf("space %q did not fold: %q", ws, headerMatchKey("金"+ws+"额"))
		}
	}
}

// TestHeaderMatchKeyParenWidthFolds pins the #33 F2 normalization on the
// concrete family that paid for its absence (#29): both paren widths fold
// to the half-width form, mixed strings included.
func TestHeaderMatchKeyParenWidthFolds(t *testing.T) {
	if headerMatchKey("电费（元）") != "电费(元)" {
		t.Fatalf("full-width parens must fold: %q", headerMatchKey("电费（元）"))
	}
	if headerMatchKey("（出账费用）") != "(出账费用)" {
		t.Fatalf("wrapped form must fold: %q", headerMatchKey("（出账费用）"))
	}
	if headerMatchKey("（") != "(" || headerMatchKey("）") != ")" {
		t.Fatalf("lone parens must fold: %q %q", headerMatchKey("（"), headerMatchKey("）"))
	}
}

// TestHeaderMatchKeyMixedCaseLowercases pins invariant 3 on the English
// and mixed forms (#65 P3-d's families).
func TestHeaderMatchKeyMixedCaseLowercases(t *testing.T) {
	if headerMatchKey("Amount(USD)") != "amount(usd)" {
		t.Fatalf("mixed-case header must lowercase: %q", headerMatchKey("Amount(USD)"))
	}
}

// ---- the deduped tower 口径 table (#33a's regression pin) ----

// TestTowerSynonymsDedupedMatchesFullWidthHeader pins the dedup: the
// scoped table now lists each 口径 ONCE in half-width form, and a
// FULL-WIDTH header must still match it (paren normalization carries the
// load the hand-duplicated entries used to carry). This is the exact
// regression that would reopen #29 if it regressed.
func TestTowerSynonymsDedupedMatchesFullWidthHeader(t *testing.T) {
	fullWidth := []string{"站址编码", "产品服务费合计（出账费用）（不含税）", "罚责赠费合计"}
	halfWidth := []string{"站址编码", "产品服务费合计(出账费用)(不含税)", "罚责赠费合计"}
	for _, headers := range [][]string{fullWidth, halfWidth} {
		h, ok, variants := RecognizeAmountForType(FeeTypeTowerLease, headers)
		if !ok || h != headers[1] || variants != 1 {
			t.Fatalf("scoped match failed on %v: got %q hit=%v variants=%d (罚责赠费合计 carries no stem)", headers, h, ok, variants)
		}
	}
}

// TestTowerSynonymsPriorityOrder pins the 口径 priority: the
// （出账费用）（不含税） form wins over the bare 产品服务费合计 when a sheet
// carries both.
func TestTowerSynonymsPriorityOrder(t *testing.T) {
	headers := []string{
		"站址编码",
		"产品服务费合计",
		"产品服务费合计（出账费用）（不含税）",
	}
	h, ok, _ := RecognizeAmountForType(FeeTypeTowerLease, headers)
	if !ok || h != headers[2] {
		t.Fatalf("口径 priority broken: chose %q (hit=%v), want the 不含税 form", h, ok)
	}
}

// TestTowerSynonymsDecoyVariantsNeverWin pins the #29 decoy contract from
// the scoped table's side: the all-zero sibling variants never resolve as
// the amount while a legal 口径 form exists.
func TestTowerSynonymsDecoyVariantsNeverWin(t *testing.T) {
	headers := []string{
		"站址编码",
		"产品服务费合计(出账费用)(历史调增)", // the decoy family, NOT in the scoped table
		"产品服务费合计(历史待退回)",      // the other decoy
	}
	if h, ok, _ := RecognizeAmountForType(FeeTypeTowerLease, headers); ok {
		t.Fatalf("decoy variant matched the scoped table: %q", h)
	}
}

// TestRecognizeAmountForTypeStemCount pins the variants counter the
// multi-口径 warning reads: a sheet carrying the whole stem family counts
// every stem-sharing header.
func TestRecognizeAmountForTypeStemCount(t *testing.T) {
	headers := []string{
		"站址编码",
		"产品服务费合计(出账费用)(历史调增)",
		"产品服务费合计(历史待退回)",
		"产品服务费合计(出账费用)(不含税)",
	}
	_, ok, variants := RecognizeAmountForType(FeeTypeTowerLease, headers)
	if !ok || variants != 3 {
		t.Fatalf("hit=%v variants=%d, want true/3 (three stem-sharing headers)", ok, variants)
	}
}

// TestRecognizeAmountForTypeMissReturnsZero pins the miss contract: an
// unscoped type or a no-match header set answers the zero triple, and
// the caller falls back to the generic recognizer.
func TestRecognizeAmountForTypeMissReturnsZero(t *testing.T) {
	if h, ok, v := RecognizeAmountForType(FeeTypeElectricity, []string{"站址编码", "电费(元)"}); ok || h != "" || v != 0 {
		t.Fatalf("unscoped type answered a hit: %q %v %d", h, ok, v)
	}
	if h, ok, v := RecognizeAmountForType(FeeTypeTowerLease, []string{"站址编码"}); ok || h != "" || v != 0 {
		t.Fatalf("no-amount sheet answered a hit: %q %v %d", h, ok, v)
	}
}

// ---- NormalizeAmount families ----

// TestNormalizeAmountParenPairWidthMustMatch pins the wrap contract: the
// accounting minus is a MATCHED pair — 「（50)」 is refused, both widths'
// matched pairs parse as negative.
func TestNormalizeAmountParenPairWidthMustMatch(t *testing.T) {
	for _, bad := range []string{"（50)", "(50）", "（50.00)", "(50.00）"} {
		if _, err := NormalizeAmount(bad); err == nil {
			t.Errorf("mixed-width wrap %q must be refused", bad)
		}
	}
	// a matched half-width pair carrying a FULL-WIDTH digit is legal — the
	// width mixing that matters is the WRAP's; the digits normalize.
	if c, err := NormalizeAmount("(５0)"); err != nil || c != -5000 {
		t.Errorf("(５0) = (%d, %v), want -5000 (matched pair, digit width folds)", c, err)
	}
	if c, err := NormalizeAmount("(50.00)"); err != nil || c != -5000 {
		t.Errorf("(50.00) = (%d, %v), want -5000", c, err)
	}
	if c, err := NormalizeAmount("（50.00）"); err != nil || c != -5000 {
		t.Errorf("（50.00） = (%d, %v), want -5000", c, err)
	}
}

// TestNormalizeAmountCurrencyAndUnitStripped pins the leading strip:
// currency signs and the trailing 元 leave before the wrap check, so
// 「¥(25.00)」 and 「(¥25.00)」 both mean minus.
func TestNormalizeAmountCurrencyAndUnitStripped(t *testing.T) {
	for _, s := range []string{"¥(25.00)", "(¥25.00)", "￥(25.00)", "(30.00)元"} {
		if _, err := NormalizeAmount(s); err != nil {
			t.Errorf("%q must parse (currency stripped first): %v", s, err)
		}
	}
	// a space between the sign and the wrap defeats the wrap check — the
	// strip leaves them non-adjacent and the read refuses.
	if _, err := NormalizeAmount("$ (30.00)"); err == nil {
		t.Error("$ (30.00) must refuse — the wrap check sees the space, not the paren")
	}
	if c, err := NormalizeAmount("¥(25.00)"); err != nil || c != -2500 {
		t.Errorf("¥(25.00) = (%d, %v), want -2500", c, err)
	}
}

// TestNormalizeAmountGroupedThousands pins the accounting grouping: comma
// families (both widths, with internal spaces) fold away.
func TestNormalizeAmountGroupedThousands(t *testing.T) {
	for _, s := range []string{"1,234.56", "1，234.56", "1,234,567.89", "1 234.56", "1, 234.56"} {
		if _, err := NormalizeAmount(s); err != nil {
			t.Errorf("%q must parse: %v", s, err)
		}
	}
	if c, err := NormalizeAmount("1,234,567.89"); err != nil || c != 123456789 {
		t.Errorf("1,234,567.89 = (%d, %v), want 123456789 cents", c, err)
	}
}

// TestNormalizeAmountFullWidthDigits pins the full-width family: digits,
// dot and minus all fold to their half-width twins.
func TestNormalizeAmountFullWidthDigits(t *testing.T) {
	if c, err := NormalizeAmount("０１２"); err != nil || c != 1200 {
		t.Errorf("０１２ = (%d, %v), want 1200 cents (the fraction loop scales a bare integer by two digits)", c, err)
	}
	if c, err := NormalizeAmount("１２．３４"); err != nil || c != 1234 {
		t.Errorf("１２．３４ = (%d, %v), want 1234 cents", c, err)
	}
	if c, err := NormalizeAmount("－５"); err != nil || c != -500 {
		t.Errorf("－５ = (%d, %v), want -500 cents", c, err)
	}
}

// TestNormalizeAmountFractionScaling pins the two-digit scaling: 「.5」 is
// 50 cents, 「5.」 is 500, and a THIRD fraction digit is refused (never
// silently rounded — a mangled amount is a false risk signal).
func TestNormalizeAmountFractionScaling(t *testing.T) {
	if c, err := NormalizeAmount(".5"); err != nil || c != 50 {
		t.Errorf(".5 = (%d, %v), want 50", c, err)
	}
	if c, err := NormalizeAmount("5."); err != nil || c != 500 {
		t.Errorf("5. = (%d, %v), want 500", c, err)
	}
	if _, err := NormalizeAmount("22.685"); err == nil {
		t.Error("a third fraction digit must be refused (拒绝静默取整)")
	}
	if _, err := NormalizeAmount("22.68.01"); err == nil {
		t.Error("two dots must be refused")
	}
}

// TestNormalizeAmountRefusalFamily pins the refuse-to-guess family:
// scientific notation, mid-string parens, double minus, empty and pure
// garbage all answer errors.
func TestNormalizeAmountRefusalFamily(t *testing.T) {
	for _, s := range []string{"1e3", "1E999", "1(2)3", "(50.00", "--5", "", "  ", "abc", "１e3"} {
		if _, err := NormalizeAmount(s); err == nil {
			t.Errorf("%q must be refused", s)
		}
	}
}

// TestNormalizeAmountOverflowBoundary pins the int64-cents ceiling: the
// last representable value parses, one cent past it refuses.
func TestNormalizeAmountOverflowBoundary(t *testing.T) {
	legal := "92233720368547758.07" // MaxInt64/100 = 92233720368547758.07 → 9223372036854775807 cents
	if c, err := NormalizeAmount(legal); err != nil || c != 9223372036854775807 {
		t.Errorf("%s = (%d, %v), want MaxInt64 cents", legal, c, err)
	}
	if _, err := NormalizeAmount("92233720368547758.08"); err == nil {
		t.Error("one cent past MaxInt64 must refuse (超出可表示范围)")
	}
}

// TestNormalizeAmountNegativeZero pins the zero identities: every zero
// spelling — bare, signed, wrapped, full-width — parses as exactly 0.
func TestNormalizeAmountNegativeZero(t *testing.T) {
	for _, s := range []string{"0", "0.00", "-0", "(0)", "（0）", "０", "-0.00"} {
		if c, err := NormalizeAmount(s); err != nil || c != 0 {
			t.Errorf("%q = (%d, %v), want 0/nil", s, c, err)
		}
	}
}

// ---- ParsePeriod / ParseDatePeriod families ----

// TestParsePeriodAcceptedFamilies pins every accepted spelling of a
// period: dash, compact, 年月, dot, slash.
func TestParsePeriodAcceptedFamilies(t *testing.T) {
	for _, c := range []struct {
		in   string
		y, m int
	}{
		{"2026-01", 2026, 1},
		{"2026-1", 2026, 1},
		{"202601", 2026, 1},
		{"2026年1月", 2026, 1},
		{"2026年01月", 2026, 1},
		{"2026.3", 2026, 3},
		{"2026/12", 2026, 12},
	} {
		y, m, err := ParsePeriod(c.in)
		if err != nil || y != c.y || m != c.m {
			t.Errorf("ParsePeriod(%q) = (%d,%d,%v), want (%d,%d)", c.in, y, m, err, c.y, c.m)
		}
	}
}

// TestParsePeriodTrailingDayDropped pins the day-dropping rule: a full
// date is a legal period declaration, the day is discarded (the grain is
// the month).
func TestParsePeriodTrailingDayDropped(t *testing.T) {
	y, m, err := ParsePeriod("2026-01-15")
	if err != nil || y != 2026 || m != 1 {
		t.Errorf("ParsePeriod(\"2026-01-15\") = (%d,%d,%v), want (2026,1)", y, m, err)
	}
}

// TestParsePeriodBoundsRefused pins the CHECK bounds at the parser: years
// outside 2000..2100 and months outside 1..12 refuse here, not at the
// partition layer.
func TestParsePeriodBoundsRefused(t *testing.T) {
	for _, s := range []string{"1999-12", "2101-01", "2026-00", "2026-13", "999-01", "2026013", "账单日期", ""} {
		if _, _, err := ParsePeriod(s); err == nil {
			t.Errorf("ParsePeriod(%q) must refuse (outside CHECK bounds)", s)
		}
	}
}

// TestParseDatePeriodAcceptedFamilies pins the date channel: dash, slash,
// dot and 年月日 spellings all land on the month grain.
func TestParseDatePeriodAcceptedFamilies(t *testing.T) {
	for _, c := range []struct {
		in   string
		y, m int
	}{
		{"2024-05-17", 2024, 5},
		{"2024/5/7", 2024, 5},
		{"2024.5.7", 2024, 5},
		{"2024年5月17日", 2024, 5},
		{"2024年5月", 2024, 5},
	} {
		y, m, err := ParseDatePeriod(c.in)
		if err != nil || y != c.y || m != c.m {
			t.Errorf("ParseDatePeriod(%q) = (%d,%d,%v), want (%d,%d)", c.in, y, m, err, c.y, c.m)
		}
	}
}

// ---- NormalizeSiteCode family ----

// TestSiteCodeWhitespaceCollapseFamilies pins the identity-string rules:
// all whitespace families collapse, leading zeros survive, and the result
// carries no whitespace (idempotent).
func TestSiteCodeWhitespaceCollapseFamilies(t *testing.T) {
	if got := NormalizeSiteCode(" AB 0012 \tCD "); got != "AB0012CD" {
		t.Errorf("collapse = %q, want AB0012CD", got)
	}
	if got := NormalizeSiteCode("　012　"); got != "012" {
		t.Errorf("full-width spaces + leading zero: %q, want 012", got)
	}
	if got := NormalizeSiteCode("\u00a0007\u00a0"); got != "007" {
		t.Errorf("NBSP: %q, want 007", got)
	}
}

// ---- reassembleThousands families (#98) ----

// TestReassembleThousandsGreedyOrder pins the greedy-leftmost discipline:
// the FIRST mergeable pair merges, one pair per pass.
func TestReassembleThousandsGreedyOrder(t *testing.T) {
	out, ok := reassembleThousands([]string{"1", "234", "9", "567.89"}, 3)
	if !ok || len(out) != 3 {
		t.Fatalf("greedy merge failed: %v ok=%v", out, ok)
	}
	if out[0] != "1234" {
		t.Errorf("leftmost pair must merge first: %v", out)
	}
}

// TestReassembleThousandsChainedMerges pins the chain: a merged head is a
// legal head for the NEXT pass, so 「1」+「234」 then 「1234」+「567.89」
// completes in one call.
func TestReassembleThousandsChainedMerges(t *testing.T) {
	out, ok := reassembleThousands([]string{"1", "234", "567", "890.12"}, 2)
	if !ok || len(out) != 2 {
		t.Fatalf("chain merge failed: %v ok=%v", out, ok)
	}
	if out[0] != "1234567" || out[1] != "890.12" {
		t.Errorf("chained result = %v", out)
	}
}

// TestReassembleThousandsLoudRefusal pins the refusal: a row no accounting
// hypothesis can reassemble answers ok=false — the caller fails LOUD, the
// misparse never reaches the analysis table silently.
func TestReassembleThousandsLoudRefusal(t *testing.T) {
	if _, ok := reassembleThousands([]string{"1", "23", "4"}, 2); ok {
		t.Error("unmergeable row must refuse (no 1-3 head + exactly-3 tail pair)")
	}
	if out, ok := reassembleThousands([]string{"a", "b", "c"}, 2); ok {
		t.Errorf("garbage row must refuse, got %v", out)
	}
}

// ---- readCSV families ----

// writeRawCSV writes arbitrary bytes as a CSV file (the reader tests'
// seeder — bytes go in verbatim, including BOMs and blank lines).
func writeRawCSV(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := testsupport.TempDir(t)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReadCSVBOMTolerated pins the #98 contract at the family level: the
// UTF-8 BOM strips before the parser, so the first header never carries
// \ufeff and recognition is not defeated by an Excel export.
func TestReadCSVBOMTolerated(t *testing.T) {
	p := writeRawCSV(t, "bom.csv", []byte("\xef\xbb\xbf站址编码,电费(元)\nS-1,10.00\n"))
	headers, rows, err := readCSV(p)
	if err != nil {
		t.Fatal(err)
	}
	if headers[0] != "站址编码" {
		t.Fatalf("BOM survived into the header: %q", headers[0])
	}
	if len(rows) != 1 || rows[0].Cells[0] != "S-1" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestReadCSVBlankRowsSkipped pins the blank-row contract: empty lines and
// all-empty lines never become phantom data rows, and the row numbers
// stay the SOURCE numbering (the evidence coordinate).
func TestReadCSVBlankRowsSkipped(t *testing.T) {
	p := writeRawCSV(t, "blank.csv", []byte("站址编码,电费(元)\n\n,,\nS-1,10.00\n"))
	headers, rows, err := readCSV(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 2 || len(rows) != 1 {
		t.Fatalf("headers=%d rows=%d, want 2/1", len(headers), len(rows))
	}
	if rows[0].Number != 3 {
		t.Errorf("row number = %d, want 3 (source numbering, blank lines counted)", rows[0].Number)
	}
}

// TestReadCSVShortRowKeepsEmptyCells pins the ragged contract: shorter
// rows keep their tolerant shape (missing trailing optional columns stay
// legal as ""), the header count is the ceiling.
func TestReadCSVShortRowKeepsEmptyCells(t *testing.T) {
	p := writeRawCSV(t, "short.csv", []byte("站址编码,账期,电费(元)\nS-1,2026-01\n"))
	headers, rows, err := readCSV(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Cells) != 2 {
		t.Fatalf("short row = %+v", rows)
	}
	_ = headers
}

// TestReadCSVLoudRefusalOnUnmergeableRow pins the #98 error path: a row
// with more cells than the header that no thousands-hypothesis can
// reassemble fails the WHOLE read with the row number — never a silent
// misaligned import.
func TestReadCSVLoudRefusalOnUnmergeableRow(t *testing.T) {
	p := writeRawCSV(t, "misaligned.csv", []byte("站址编码,电费(元)\nS-1,10.00\n1,23,4\n"))
	_, _, err := readCSV(p)
	if err == nil {
		t.Fatal("the unmergeable longer row must fail the read loudly")
	}
	if !strings.Contains(err.Error(), "第 3 行") || !strings.Contains(err.Error(), "千分位") {
		t.Errorf("refusal must name the row and the hypothesis: %v", err)
	}
}

// TestReadCSVQuotedThousandsFine pins the happy twin of the #98 shape: a
// PROPERLY quoted thousands separator never splits — the row reads as
// two cells and the amount imports at full value.
func TestReadCSVQuotedThousandsFine(t *testing.T) {
	p := writeRawCSV(t, "quoted.csv", []byte("站址编码,电费(元)\nS-1,\"1,234.56\"\n"))
	headers, rows, err := readCSV(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 2 || len(rows) != 1 || len(rows[0].Cells) != 2 {
		t.Fatalf("quoted row shape: %+v", rows)
	}
	if rows[0].Cells[1] != "1,234.56" {
		t.Errorf("quoted field must stay whole: %q", rows[0].Cells[1])
	}
}
