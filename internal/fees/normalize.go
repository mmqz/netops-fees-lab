package fees

import (
	"fmt"
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

// NormalizeAmount parses a fee amount: strips currency marks (¥/￥/$),
// thousands separators (half- and full-width commas), spaces (half- and
// full-width) and trailing 元; accepts a leading minus and full-width
// digits/dot. Refuses anything else (empty, trailing garbage, double
// dots).
func NormalizeAmount(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	s = strings.NewReplacer(
		"¥", "", "￥", "", "$", "",
		",", "", "，", "",
		" ", "", "　", "",
		"元", "",
		"（", "", "）", "",
		"(", "", ")", "",
	).Replace(s)
	if s == "" {
		return 0, fmt.Errorf("空金额")
	}
	// full-width digits and dot to half-width
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
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("数值 %q 无法解析", raw)
	}
	return v, nil
}

// NormalizeSiteCode trims and collapses internal whitespace of a site
// code. Leading zeros are PRESERVED (the code is an identity string, never
// a number — the same doctrine as the machine_room/site 12/17-digit IDs);
// a numeric re-parse is deliberately absent.
func NormalizeSiteCode(raw string) string {
	fields := strings.Fields(strings.TrimSpace(raw))
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
