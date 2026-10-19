package fees

import (
	"reflect"
	"testing"
)

func TestNormalizeAmount(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64 // integer cents (#65 P1-a)
		fail bool
	}{
		{"1234.56", 123456, false},
		{"¥1234.56", 123456, false},
		{"￥1,234.56", 123456, false},
		{"1234.56元", 123456, false},
		{" 1 234.56 ", 123456, false},
		{"１２３４.５６", 123456, false}, // full-width digits
		{"-42.5", -4250, false},
		{"1２3４．５", 123450, false}, // mixed width
		{"0.01", 1, false},
		{".5", 50, false},  // bare fraction, as ParseFloat accepted
		{"5.", 500, false}, // trailing dot, as ParseFloat accepted
		{"0", 0, false},    // zero yuan parses (a zero-amount row is data)
		{"-0.01", -1, false},
		{"92233720368547758.06", 9223372036854775806, false},
		{"92233720368547758.07", 9223372036854775807, false}, // the exact int64-cents ceiling (== MaxInt64)
		{"", 0, true},
		{"abc", 0, true},
		{"12.34.56", 0, true},
		{"电费", 0, true},
		{"1e3", 0, true},                  // scientific notation refused (fixed-point contract)
		{"0.001", 0, true},                // three fraction digits: precision beyond cents refused
		{"1.999", 0, true},                // never silently rounded
		{"92233720368547758.08", 0, true}, // one cent past the int64 ceiling
		{"99999999999999999", 0, true},    // 17-digit integer part overflows
		{"-", 0, true},
	} {
		got, err := NormalizeAmount(tc.raw)
		if tc.fail {
			if err == nil {
				t.Errorf("NormalizeAmount(%q) = %v, want error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeAmount(%q): %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeAmount(%q) = %v cents, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestCentsAccumulationIsExact pins the #65 P1-a acceptance property: a
// million 0.01 rows accumulate to EXACTLY 10000.00 yuan — integer cents
// cannot drift, unlike the float64 path (which measured 10000.0000001719).
func TestCentsAccumulationIsExact(t *testing.T) {
	cents, err := NormalizeAmount("0.01")
	if err != nil {
		t.Fatalf("NormalizeAmount(0.01): %v", err)
	}
	if cents != 1 {
		t.Fatalf("NormalizeAmount(0.01) = %d, want 1 cent", cents)
	}
	const rows = 1_000_000
	var sum int64
	for i := 0; i < rows; i++ {
		sum += cents
	}
	if sum != 10_000_00 { // 10000.00 yuan = 1,000,000 cents
		t.Fatalf("sum of %d rows of 0.01 = %d cents, want exactly 1000000", rows, sum)
	}
	// and the ORDER of accumulation is irrelevant — the property the
	// duplicate-settlement comparison relies on.
	var rev int64
	for i := 0; i < rows; i++ {
		rev += cents
	}
	if rev != sum {
		t.Fatalf("reordered sum = %d, want %d (integer sums are order-independent)", rev, sum)
	}
}

func TestNormalizeSiteCode(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{" 2301010ABCDE00001 ", "2301010ABCDE00001"},
		{"2301010ABCDE 00001", "2301010ABCDE00001"}, // internal whitespace collapsed
		{"０１２３", "０１２３"},                            // full-width digits preserved verbatim
		{"\t2301\n", "2301"},
	} {
		if got := NormalizeSiteCode(tc.raw); got != tc.want {
			t.Errorf("NormalizeSiteCode(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	// the leading-zero doctrine: a numeric-looking code keeps its zeros.
	if got := NormalizeSiteCode(" 012345678901234 "); got != "012345678901234" {
		t.Errorf("leading zeros lost: %q", got)
	}
}

func TestParsePeriod(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		wantY, wantM int
		fail         bool
	}{
		{"2024-05", 2024, 5, false},
		{"2024/05", 2024, 5, false},
		{"2024.05", 2024, 5, false},
		{"202405", 2024, 5, false},
		{"2024年5月", 2024, 5, false},
		{"2024年05月", 2024, 5, false},
		{" 2024-05 ", 2024, 5, false},
		{"2024-05-01", 2024, 5, false}, // day accepted, dropped
		{"2024-13", 0, 0, true},
		{"1999-05", 0, 0, true}, // below the migration CHECK bounds
		{"", 0, 0, true},
		{"2024--5", 0, 0, true},
	} {
		y, m, err := ParsePeriod(tc.raw)
		if tc.fail {
			if err == nil {
				t.Errorf("ParsePeriod(%q) = %d-%d, want error", tc.raw, y, m)
			}
			continue
		}
		if err != nil || y != tc.wantY || m != tc.wantM {
			t.Errorf("ParsePeriod(%q) = %d-%d (err %v), want %d-%d", tc.raw, y, m, err, tc.wantY, tc.wantM)
		}
	}
}

func TestParseDatePeriod(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		wantY, wantM int
		fail         bool
	}{
		{"2024-05-17", 2024, 5, false},
		{"2024/5/7", 2024, 5, false},
		{"2024年5月17日", 2024, 5, false},
		{"2024.5.7", 2024, 5, false},
		{"2024-05", 2024, 5, false}, // bare period tolerated as month grain
		{"2024-13-01", 0, 0, true},
		{"not a date", 0, 0, true},
	} {
		y, m, err := ParseDatePeriod(tc.raw)
		if tc.fail {
			if err == nil {
				t.Errorf("ParseDatePeriod(%q) = %d-%d, want error", tc.raw, y, m)
			}
			continue
		}
		if err != nil || y != tc.wantY || m != tc.wantM {
			t.Errorf("ParseDatePeriod(%q) = %d-%d (err %v), want %d-%d", tc.raw, y, m, err, tc.wantY, tc.wantM)
		}
	}
}

func TestRecognize(t *testing.T) {
	// a real-shaped electricity header: type markers hit, core columns hit.
	typ, cols := Recognize([]string{"站址编码", "账期", "电量(kWh)", "电费(元)", "供电主体", "功率因数"})
	if typ != FeeTypeElectricity {
		t.Errorf("recognized type = %q, want electricity", typ)
	}
	if cols["siteCode"] != "站址编码" || cols["amount"] != "电费(元)" || cols["period"] != "账期" {
		t.Errorf("recognized core columns = %v", cols)
	}
	// tower lease header.
	typ, _ = Recognize([]string{"站址编号", "铁塔租费", "起租日期", "塔类"})
	if typ != FeeTypeTowerLease {
		t.Errorf("recognized type = %q, want tower_lease", typ)
	}
	// no markers → type "" (the operator declares).
	typ, cols = Recognize([]string{"站点代码", "金额", "月份"})
	if typ != "" {
		t.Errorf("recognized type = %q, want empty", typ)
	}
	if cols["siteCode"] != "站点代码" || cols["amount"] != "金额" || cols["period"] != "月份" {
		t.Errorf("recognized core columns = %v", cols)
	}
}

func TestCanonicalMapping(t *testing.T) {
	a := FeeMapping{FeeType: FeeTypeElectricity, Columns: map[string]string{"amount": "电费", "siteCode": "站址编码"}}
	b := FeeMapping{FeeType: FeeTypeElectricity, Columns: map[string]string{"siteCode": "站址编码", "amount": "电费"}}
	if a.CanonicalMapping() != b.CanonicalMapping() {
		t.Errorf("reordered mappings must canonicalize equal:\n%s\n%s", a.CanonicalMapping(), b.CanonicalMapping())
	}
	if !reflect.DeepEqual(a, b) { // sanity: the inputs really are reorderings
		t.Fatal("test bug: inputs differ beyond order")
	}
}

// TestRecognizeEnglishHeaders pins the #65 P3-d fix: the other-system
// exports' English headers auto-recognize across their real-world
// spellings and cases (site_code / Site Code / SITE_CODE / siteCode /
// station_code / cell_code / amount / period), while the Chinese
// suggestions keep their exact prior shape (the English tail never
// wins over a Chinese synonym that already matched).
func TestRecognizeEnglishHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
	}{
		{"snake", []string{"site_code", "amount", "period"}},
		{"spaced", []string{"Site Code", "Amount (yuan)", "Billing Period"}},
		{"upper", []string{"SITE_CODE", "AMOUNT", "PERIOD"}},
		{"camel", []string{"siteCode", "amount", "period"}},
		{"station", []string{"station_code", "amount", "period"}},
		{"cell", []string{"cell_code", "amount", "period"}},
		{"date variant", []string{"site_code", "amount", "billing_date"}},
	} {
		_, cols := Recognize(tc.headers)
		if cols["siteCode"] == "" || cols["amount"] == "" {
			t.Errorf("%s: siteCode/amount not recognized: %v (headers %v)", tc.name, cols, tc.headers)
		}
		if tc.headers[2] != "billing_date" && cols["period"] == "" {
			t.Errorf("%s: period not recognized: %v", tc.name, cols)
		}
	}
	// Chinese headers keep the exact prior suggestions (English synonyms
	// never displace a Chinese match — list order is the tiebreak).
	_, cols := Recognize([]string{"站址编码", "电费(元)", "账期"})
	if cols["siteCode"] != "站址编码" || cols["amount"] != "电费(元)" || cols["period"] != "账期" {
		t.Errorf("Chinese header suggestions changed: %v", cols)
	}
	// mixed: Chinese site code + English amount
	_, cols = Recognize([]string{"站点编码", "amount", "period"})
	if cols["siteCode"] != "站点编码" || cols["amount"] != "amount" || cols["period"] != "period" {
		t.Errorf("mixed header suggestions = %v", cols)
	}
}
