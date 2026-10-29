package fees

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The NORMALIZATION layer (#61's fee-domain specifics): amounts, periods
// and site codes arrive in every shape real telecom finance files produce
// (full-width yen, thousands separators, 2024年5月, 15-digit codes with
// leading zeros). Every parse is strict and refuses rather than guesses —
// a mangled amount is a false risk signal, a wrong period is a wrong
// partition, and both poison the analysis. Raw values stay in the
// snapshot; only provably-normalized values reach the analysis table.

// NormalizeAmount parses a fee amount into INTEGER CENTS (#65 P1-a): money
// is stored, summed and compared as int64 cents end-to-end (parquet
// amount_cents, sum() in DuckDB, JSON totalCents) — a float64 anywhere in
// that chain leaks binary-representation error into reconciliation
// (accumulating 0.01 a million times drifts, and two batch sums of the
// same rows in different orders differ), which is exactly the class of
// false signal the risk module must never emit. The parse is therefore
// FIXED-POINT digit-by-digit: the value never passes through a float.
//
// Accepted shapes: the currency/separator stripping of the old parser
// (¥/￥/$, half/full-width commas, spaces, trailing 元, full-width digits
// and dot), the ACCOUNTING NEGATIVE — a fully parenthesized value
// "(50.00)"/“（50.00）” means minus (#98: the old parser stripped the
// parentheses and parsed (50.00) as POSITIVE 5000 cents, flipping credit
// signs) — then [−]?digits[.digits] with AT MOST TWO fraction digits — a
// third fraction digit is refused (never silently rounded: a mangled
// amount is a false risk signal, the normalize layer refuses rather than
// guesses). Scientific notation is refused as well (real fee files never
// carry it; ParseFloat's acceptance of 1e3 was an accident of the float
// path, not a contract). Parentheses anywhere else — "(50.00", "1(2)3" —
// are refused: the wrap is the only legal paren shape.
func NormalizeAmount(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	// currency signs and the trailing unit strip FIRST so the wrap check
	// also sees "¥(25.00)" and "(¥25.00)" in their bare paren shape.
	s = strings.NewReplacer("¥", "", "￥", "", "$", "", "元", "").Replace(s)
	// accounting negative next: (…) / （…） — the full wrap means minus.
	// Either paren family, but the pair must MATCH (mixed "（50)" is not a
	// shape real files produce — refuse rather than guess).
	neg := false
	if len(s) >= 3 {
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			neg = true
			s = s[1 : len(s)-1]
		} else if strings.HasPrefix(s, "（") && strings.HasSuffix(s, "）") {
			neg = true
			s = s[len("（") : len(s)-len("）")]
		}
	}
	if strings.ContainsAny(s, "()（）") {
		return 0, fmt.Errorf("数值 %q 无法解析（括号仅可整体包裹表示负数）", raw)
	}
	s = strings.NewReplacer(
		",", "", "，", "",
		" ", "", "　", "",
	).Replace(s)
	if s == "" {
		return 0, fmt.Errorf("空金额")
	}
	// full-width digits, dot and minus to half-width
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			b.WriteRune(r - '０' + '0')
		case r == '．':
			b.WriteByte('.')
		case r == '－':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	s = b.String()

	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
		if s == "" {
			return 0, fmt.Errorf("数值 %q 无法解析", raw)
		}
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return 0, fmt.Errorf("数值 %q 无法解析", raw)
		}
	}
	if len(fracPart) > 2 {
		return 0, fmt.Errorf("金额 %q 精度超过分（最多两位小数）——拒绝静默取整", raw)
	}
	if intPart == "" && fracPart == "" {
		return 0, fmt.Errorf("数值 %q 无法解析", raw)
	}

	const maxCents = math.MaxInt64
	cents := int64(0)
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("数值 %q 无法解析", raw)
		}
		d := int64(r - '0')
		if cents > (maxCents-d)/10 {
			return 0, fmt.Errorf("金额 %q 超出可表示范围", raw)
		}
		cents = cents*10 + d
	}
	// fraction: scale to exactly two digits (".5" → 50, ".56" → 56)
	for i := 0; i < 2; i++ {
		var d int64
		if i < len(fracPart) {
			r := fracPart[i]
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("数值 %q 无法解析", raw)
			}
			d = int64(r - '0')
		}
		if cents > (maxCents-d)/10 {
			return 0, fmt.Errorf("金额 %q 超出可表示范围", raw)
		}
		cents = cents*10 + d
	}
	if neg {
		cents = -cents
	}
	return cents, nil
}

// NormalizeSiteCode trims and collapses internal whitespace of a site
// code. Leading zeros are PRESERVED (the code is an identity string, never
// a number — the same doctrine as the machine_room/site 12/17-digit IDs);
// a numeric re-parse is deliberately absent.
func NormalizeSiteCode(raw string) string {
	// Invalid UTF-8 is sanitized BEFORE the field split (nightly fuzz
	// FuzzNormalizeSiteCode, seed a1adba6a): a lone lead byte and a lone
	// continuation byte with whitespace between them ("\xc2" + " " +
	// "\xa0") used to GLUE into a different valid character when the
	// separator dropped — pass one produced U+00A0, pass two stripped it
	// (NBSP IS unicode whitespace), breaking the idempotency the site-code
	// key contract needs. RuneError is not whitespace and never
	// recombines, so the output is stable under re-normalization.
	valid := strings.ToValidUTF8(raw, "\uFFFD")
	fields := strings.Fields(strings.TrimSpace(valid))
	return strings.Join(fields, "")
}

// ParsePeriod derives (year, month) from a period expression: 2024-05,
// 2024/05, 2024.05, 202405, 2024年5月, 2024年05月, 2024-05-01 (a day is
// accepted and dropped — the partition grain is the month). Returns an
// error on anything ambiguous or out of range.
func ParsePeriod(raw string) (int, int, error) {
	s := strings.TrimSpace(raw)
	s = strings.NewReplacer("年", "-", "月", "", ".", "-", "/", "-", "　", "").Replace(s)
	s = strings.ReplaceAll(s, " ", "")
	// drop a trailing day: 2024-05-01 → 2024-05
	if parts := strings.Split(s, "-"); len(parts) == 3 {
		s = parts[0] + "-" + parts[1]
	}
	if len(s) == 6 && !strings.Contains(s, "-") { // 202405
		if y, m := atoi2(s[:4], s[4:]); validPeriod(y, m) {
			return y, m, nil
		}
		return 0, 0, fmt.Errorf("账期 %q 不在合法范围", raw)
	}
	if parts := strings.Split(s, "-"); len(parts) == 2 { // 2024-05 / 2024-5
		if y, m := atoi2(parts[0], parts[1]); validPeriod(y, m) {
			return y, m, nil
		}
		return 0, 0, fmt.Errorf("账期 %q 不在合法范围", raw)
	}
	return 0, 0, fmt.Errorf("账期 %q 无法解析（可用形如 2024-05 / 202405 / 2024年5月）", raw)
}

// ParseDatePeriod derives (year, month) from a full date (2024-05-17,
// 2024/5/7, 2024年5月17日): the grain is the month, the day is dropped.
func ParseDatePeriod(raw string) (int, int, error) {
	s := strings.TrimSpace(raw)
	s = strings.NewReplacer("年", "-", "月", "-", "日", "", "号", "", "　", "").Replace(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, " ", "")
	parts := strings.Split(s, "-")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, fmt.Errorf("日期 %q 无法解析", raw)
	}
	y, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("日期 %q 的年份无法解析", raw)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("日期 %q 的月份无法解析", raw)
	}
	if !validPeriod(y, m) {
		return 0, 0, fmt.Errorf("日期 %q 不在合法范围", raw)
	}
	return y, m, nil
}

// atoi2 parses a (year, month) string pair; (0,0) on any parse failure.
func atoi2(ys, ms string) (int, int) {
	y, err1 := strconv.Atoi(ys)
	m, err2 := strconv.Atoi(ms)
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return y, m
}

// validPeriod bounds the partition grain (the migration's CHECK bounds —
// 2000..2100, 1..12).
func validPeriod(y, m int) bool {
	return y >= 2000 && y <= 2100 && m >= 1 && m <= 12
}

// PeriodLabel renders a normalized period the way the partitions are
// named (2024-05) — display and directory share one form.
func PeriodLabel(year, month int) string {
	return fmt.Sprintf("%04d-%02d", year, month)
}

// ParseDateISO parses a full calendar date into the canonical ISO form
// 'YYYY-MM-DD' (issue #11's 购电起始日/购电截止日 columns). Accepted
// shapes: 2026-01-15, 2026.1.5, 2026/01/15, 2026年1月15日, 2026年1月15,
// 2026-1-5 — the SAME shape family ParseDatePeriod accepts, but the DAY
// is kept (the purchase window's whole analytic value is the day-level
// span: date_diff('day', start, end)). The date must be a real calendar
// date (Feb 30 refuses); the year bounds follow validPeriod (2000..2100).
// Output is zero-padded so every consumer (DuckDB CAST AS DATE, JSON,
// display) sees one form.
func ParseDateISO(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("日期为空")
	}
	s = strings.NewReplacer("年", "-", "月", "-", "日", "", "号", "", "　", "").Replace(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, " ", "")
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return "", fmt.Errorf("日期 %q 无法解析（可用形如 2026-01-15 / 2026.1.5 / 2026年1月15日）", raw)
	}
	y, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	d, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return "", fmt.Errorf("日期 %q 无法解析", raw)
	}
	if y < 2000 || y > 2100 {
		return "", fmt.Errorf("日期 %q 年份不在合法范围", raw)
	}
	if m < 1 || m > 12 {
		return "", fmt.Errorf("日期 %q 月份不在合法范围", raw)
	}
	if d < 1 || d > daysInMonth(y, m) {
		return "", fmt.Errorf("日期 %q 不是真实日历日期", raw)
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d), nil
}

// daysInMonth is the calendar month length (leap-aware for February).
func daysInMonth(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	}
	return 0
}
