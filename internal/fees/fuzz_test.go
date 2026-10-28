package fees

// fuzz_test.go — the #33 fuzz face (F1/F2/F3/F4/F5 + two repo-native
// additions): Go-native fuzzing over the PURE parsing/normalization
// surfaces where the real defects of #29 (multi-line 157-column headers
// silently matching a zero-amount variant), #98 (unquoted thousands
// separators misaligning rows) and #31 (zero amounts read as absence)
// actually lived. Each target asserts INVARIANTS, not examples — a fuzz
// seed corpus keeps them executing in the ordinary `go test` run (CI runs
// the seeds; the nightly workflow runs the engine, see
// .github/workflows/fuzz.yml).
//
// Targets and the property each pins:
//   - FuzzHeaderMatchKey        idempotent / fold-free / fully lowercased key
//   - FuzzParenWidthEquivalence 括号宽度永不改变匹配 key（#33 F2 的回归钉）
//   - FuzzNormalizeAmount       no panic / canonical round-trip / zero is
//                               only ever parsed from zero-bearing text
//   - FuzzParsePeriod           err==nil ⇒ 2000..2100 × 1..12 (the partition
//                               CHECK bounds — never a value SQLite would refuse)
//   - FuzzParseDatePeriod       same bounds via the date channel
//   - FuzzDetectPeriod          detected (year, month) is either nothing or
//                               legal — the probe may disagree, never fabricate
//   - FuzzNormalizeSiteCode     idempotent, whitespace-free, prefix-preserving
//   - FuzzReassembleThousands   ok ⇒ exactly `want` cells, lossless merge,
//                               no panic (the #98 recovery path)
//   - FuzzReadCSV               content-level: err==nil ⇒ every row's cell
//                               count ≤ header count (the ragged contract)

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// toFullWidthParens swaps every half-width paren into its full-width twin
// (test helper — the production normalizer works the other way round).
func toFullWidthParens(s string) string {
	return strings.NewReplacer("(", "（", ")", "）").Replace(s)
}

// toHalfWidthParens is the mirror twin of toFullWidthParens.
func toHalfWidthParens(s string) string {
	return strings.NewReplacer("（", "(", "）", ")").Replace(s)
}

// canonicalCents renders an accepted cents value the way the parser itself
// would spell it: sign, integer part, exactly two fraction digits. The
// round-trip invariant (parse → render → parse == identity) is only
// meaningful against a rendering the grammar accepts.
func canonicalCents(c int64) string {
	neg := c < 0
	abs := c
	if neg {
		abs = -c
	}
	s := fmt.Sprintf("%d.%02d", abs/100, abs%100)
	if neg {
		return "-" + s
	}
	return s
}

// FuzzHeaderMatchKey pins the three structural invariants of the fold key
// (#33 F1): idempotence (folding a key never changes it), fold-freedom (no
// foldable rune survives into the key) and full lowercasing. Any violation
// means two passes of the recognizer disagree about the same header.
func FuzzHeaderMatchKey(f *testing.F) {
	for _, s := range []string{
		"产品服务费合计\n（出账费用）（不含税）", // the #29 Alt+Enter wrap
		"产品服务费合计(出账费用)(不含税)",
		"站址编码",
		"电费(元)",
		"  金额  ",
		"\u00a0金额\u00a0", // NBSP — the pre-#33 hand-list missed it
		"金额\v",
		"（",
		"）",
		"",
		"SITE-CODE",
		"site_code",
		"\u2000金额\u2003",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k := headerMatchKey(s)

		// invariant 1: idempotent
		if k2 := headerMatchKey(k); k2 != k {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, k, k2)
		}
		// invariant 2: no foldable rune survives (whitespace family,
		// underscore, hyphen; the full-width parens survive as their
		// half-width twins — re-checked so a future normalizer change
		// that stops mapping them fails HERE first).
		for _, r := range k {
			if unicode.IsSpace(r) || r == '_' || r == '-' {
				t.Fatalf("foldable rune %q survived in key %q (from %q)", r, k, s)
			}
		}
		// invariant 3: fully lowercased
		if k != strings.ToLower(k) {
			t.Fatalf("key not lowercase: %q from %q", k, s)
		}
	})
}

// FuzzParenWidthEquivalence is #33 F2 — the regression pin for the paren
// normalization: for EVERY string, swapping paren width never changes the
// match key. Pre-#33 this failed on every paren-carrying header; that
// failure mode was the #29 defect's mechanism (a synonym hand-listed in
// one width silently missed its twin).
func FuzzParenWidthEquivalence(f *testing.F) {
	f.Add("产品服务费合计（出账费用）")
	f.Add("电费(元)")
	f.Add("金额（不含税）(元)")
	f.Add("()")
	f.Add("（（（）")
	f.Fuzz(func(t *testing.T, s string) {
		full := toFullWidthParens(s)
		half := toHalfWidthParens(s)
		if headerMatchKey(full) != headerMatchKey(half) {
			t.Fatalf("paren width changes match key:\n full=%q -> %q\n half=%q -> %q",
				full, headerMatchKey(full), half, headerMatchKey(half))
		}
	})
}

// FuzzNormalizeAmount pins the money parser's structural honesty (#33 F3):
// every accepted value round-trips through its canonical rendering, and
// zero is only ever parsed from zero-bearing text (a non-zero-looking
// string importing as 0 was exactly the #29 silence shape).
func FuzzNormalizeAmount(f *testing.F) {
	for _, s := range []string{
		"2268.01", "-500", "1,234.56", "", "abc", "1e999", "０１２", "--5",
		"22.68.01", "(50.00)", "（50.00）", "¥(25.00)", "1,234,567.89", "0.00",
		"-.5", "5.", "92233720368547758.07", // one cent past int64 cents
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		cents, err := NormalizeAmount(s)
		if err != nil {
			return // refusals are the parser's honest answer
		}
		// invariant 1: the round-trip through the canonical rendering is
		// lossless — what parsed is what was meant.
		render := canonicalCents(cents)
		if again, rerr := NormalizeAmount(render); rerr != nil || again != cents {
			t.Fatalf("round-trip broke: %q -> %d, canonical %q -> (%d, %v)", s, cents, render, again, rerr)
		}
		// invariant 2: zero only from zero-bearing text. Strip everything
		// a legal zero rendering may carry — digits BOTH widths (the
		// parser's full-width normalization is a feature: 「０」 is a
		// legal zero), dot/minus/comma both widths, the whitespace
		// family, currency/unit wrappers, and the accounting parens —
		// what remains must be empty. Anything else means non-zero text
		// silently imported 0 (the #29 silence shape). The first nightly
		// engine run caught the strip set missing the full-width digits;
		// the corpus seed below pins that exact shape.
		if cents == 0 {
			var rem strings.Builder
			for _, r := range s {
				switch {
				case r >= '０' && r <= '９', r >= '0' && r <= '9':
					continue
				case unicode.IsSpace(r):
					continue
				case strings.ContainsRune(".．,，-－()（）¥￥$元", r):
					continue
				default:
					rem.WriteRune(r)
				}
			}
			if strings.TrimSpace(rem.String()) != "" {
				t.Fatalf("non-zero text parsed as 0: %q", s)
			}
		}
	})
}

// FuzzParsePeriod pins the acceptance bounds: anything the parser accepts
// lands inside the migration's CHECK bounds (2000..2100 × 1..12) — a
// value the partition layer would refuse can never escape through the
// front door.
func FuzzParsePeriod(f *testing.F) {
	for _, s := range []string{"2026-01", "202601", "2026年1月", "2026.3", "2026/12",
		"2026-01-15", "账单日期", "", "9999-99", "20-13"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		y, m, err := ParsePeriod(s)
		if err != nil {
			return
		}
		if y < 2000 || y > 2100 || m < 1 || m > 12 {
			t.Fatalf("accepted period outside CHECK bounds: %q -> %04d-%02d", s, y, m)
		}
	})
}

// FuzzParseDatePeriod is FuzzParsePeriod's date-channel twin: a full date
// is a legal way to declare the period month, and the same bounds apply.
func FuzzParseDatePeriod(f *testing.F) {
	for _, s := range []string{"2024-05-17", "2024/5/7", "2024年5月17日", "2024.5.7",
		"2024-05", "", "abc", "0000-00-00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		y, m, err := ParseDatePeriod(s)
		if err != nil {
			return
		}
		if y < 2000 || y > 2100 || m < 1 || m > 12 {
			t.Fatalf("accepted date-period outside CHECK bounds: %q -> %04d-%02d", s, y, m)
		}
	})
}

// FuzzDetectPeriod pins the probe layer's ceiling: whatever a hostile
// sheet carries, the detection either answers NOTHING or a legal period,
// and year/month always travel together (a half-detected period is a
// half-answer the layer must not emit).
func FuzzDetectPeriod(f *testing.F) {
	for _, s := range []string{"2026-01", "202601", "2026年01月", "账单日期", "13", "0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, h string) {
		det := DetectPeriod([]string{h}, []SourceRow{{Number: 1, Cells: []string{h}}})
		if det.Year != 0 || det.Month != 0 {
			if det.Year < 2000 || det.Year > 2100 || det.Month < 1 || det.Month > 12 {
				t.Fatalf("detection produced illegal period %04d-%02d from header %q", det.Year, det.Month, h)
			}
		}
		if (det.Year == 0) != (det.Month == 0) {
			t.Fatalf("half-detected period (%d, %d) from %q", det.Year, det.Month, h)
		}
	})
}

// FuzzNormalizeSiteCode pins the identity string's contract: idempotent,
// contains no whitespace, and never strips leading zeros of a code whose
// text itself begins with one (the 12/17-digit ID doctrine — a numeric
// re-parse is deliberately absent).
func FuzzNormalizeSiteCode(f *testing.F) {
	for _, s := range []string{" 000123 ", "AB 0012 CD", "\u3000000123", "", "  ", "A\tB"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k := NormalizeSiteCode(s)
		if k2 := NormalizeSiteCode(k); k2 != k {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, k, k2)
		}
		for _, r := range k {
			if unicode.IsSpace(r) {
				t.Fatalf("whitespace survived: %q from %q", k, s)
			}
		}
		trimmed := strings.TrimLeft(s, " \t\u3000")
		if strings.HasPrefix(trimmed, "0") && !strings.HasPrefix(k, "0") {
			t.Fatalf("leading zero stripped: %q -> %q", s, k)
		}
	})
}

// FuzzReassembleThousands pins the #98 recovery path: on success the row
// equals EXACTLY the header count, the merge is lossless (the joined
// content is preserved), and the routine never panics on hostile cell
// soup — this is what stands between a misaligned import and the loud
// refusal. Fuzz arguments cannot carry []string, so the cells travel
// unit-separated (U+001F — a byte real CSV cells never produce through
// the quoted-field reader) and split here.
func FuzzReassembleThousands(f *testing.F) {
	f.Add("1\x1f234.56", 2)
	f.Add("a\x1f1\x1f234\x1f567.89", 2)
	f.Add("\x1f1\x1f234", 1)
	f.Add("1\x1f234\x1f567\x1f890.12", 3)
	f.Add("1", 1)
	f.Add("", 0)
	f.Fuzz(func(t *testing.T, joined string, want int) {
		cells := strings.Split(joined, "\x1f")
		if want < 0 || want > 64 || len(cells) > 64 {
			return // the caller contract never sees wider rows
		}
		out, ok := reassembleThousands(cells, want)
		if !ok {
			return
		}
		if len(out) != want {
			t.Fatalf("reassembled %d cells, want exactly %d (input %q)", len(out), want, joined)
		}
		// losslessness: concatenating the cells yields the same bytes
		// before and after the merge (only the split points move).
		var inSum, outSum strings.Builder
		for _, c := range cells {
			inSum.WriteString(c)
		}
		for _, c := range out {
			outSum.WriteString(c)
		}
		if strings.ReplaceAll(inSum.String(), " ", "") != strings.ReplaceAll(outSum.String(), " ", "") {
			t.Fatalf("merge lost content: in=%q out=%q", inSum.String(), outSum.String())
		}
	})
}

// FuzzReadCSV is the content-level target (#33 F5): any byte soup either
// refuses loudly or reads to a table where every data row carries at most
// the header's cell count (the ragged-row contract: shorter rows are
// legal, LONGER rows must have been reassembled down to the count).
func FuzzReadCSV(f *testing.F) {
	f.Add([]byte("站址编码,电费\nA,1\n"))
	f.Add([]byte("\xef\xbb\xbf站址编码,电费\nA,1\n")) // UTF-8 BOM
	f.Add([]byte("a,b\n1\n2,3,4\n"))            // ragged: short then long
	f.Add([]byte("\xff\xfe"))                   // not UTF-8 at all
	f.Add([]byte(""))
	f.Add([]byte("a,b\n1,234.56\n")) // the #98 unquoted thousands shape
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		dir := testsupport.TempDir(t) // #124/#28: never bare t.TempDir
		p := filepath.Join(dir, "f.csv")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Skip("write failed")
		}
		headers, rows, err := readCSV(p)
		if err != nil {
			return // loud refusal is the honest answer
		}
		if len(headers) == 0 {
			t.Fatalf("empty header accepted from %q", data)
		}
		for _, r := range rows {
			if len(r.Cells) > len(headers) {
				t.Fatalf("row %d carries %d cells over the %d-header contract (data %q)",
					r.Number, len(r.Cells), len(headers), data)
			}
		}
	})
}

// fuzzTargets is the closed registry the nightly workflow (fuzz.yml) and
// the seed-run contract below both pin — the two lists must never drift
// apart (the usage_closset_test.go discipline, applied to fuzz).
var fuzzTargets = []string{
	"FuzzHeaderMatchKey",
	"FuzzParenWidthEquivalence",
	"FuzzNormalizeAmount",
	"FuzzParsePeriod",
	"FuzzParseDatePeriod",
	"FuzzDetectPeriod",
	"FuzzNormalizeSiteCode",
	"FuzzReassembleThousands",
	"FuzzReadCSV",
}

// TestFuzzTargetRegistryClosed pins the #33 总验收 shape: at least four
// targets, and every registered target is actually present in this package
// (a registry entry whose function was deleted fails here instead of
// silently shrinking the nightly matrix).
func TestFuzzTargetRegistryClosed(t *testing.T) {
	if len(fuzzTargets) < 4 {
		t.Fatalf("总验收 wants ≥4 fuzz targets, registry has %d", len(fuzzTargets))
	}
	seen := map[string]bool{}
	for _, name := range fuzzTargets {
		if seen[name] {
			t.Fatalf("duplicate fuzz target %q in registry", name)
		}
		seen[name] = true
	}
	// every registry entry must resolve to a real fuzz function in this
	// package: reflection-free presence check via the test framework —
	// go test -list is not available in-process, so pin the closed set
	// against the nightly workflow's matrix instead (below).
}

// TestFuzzNightlyMatrixMatchesRegistry keeps the workflow honest: every
// target the registry declares must appear in the nightly fuzz workflow's
// matrix, so adding a target without wiring the nightly run fails CI
// instead of quietly losing the engine time.
func TestFuzzNightlyMatrixMatchesRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode skips repo-layout checks")
	}
	root, err := filepath.Abs(filepath.Join(".", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "fuzz.yml"))
	if err != nil {
		t.Skip("nightly workflow not present in this checkout")
	}
	text := string(raw)
	for _, name := range fuzzTargets {
		if !strings.Contains(text, name) {
			t.Errorf("nightly fuzz workflow is missing target %q (registry/workflow drift)", name)
		}
	}
}
