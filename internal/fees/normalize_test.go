package fees

import (
	"math"
	"reflect"
	"testing"
)

func TestNormalizeAmount(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
		fail bool
	}{
		{"1234.56", 1234.56, false},
		{"¥1234.56", 1234.56, false},
		{"￥1,234.56", 1234.56, false},
		{"1234.56元", 1234.56, false},
		{" 1 234.56 ", 1234.56, false},
		{"１２３４.５６", 1234.56, false}, // full-width digits
		{"-42.5", -42.5, false},
		{"1２3４．５", 1234.5, false}, // mixed width
		{"", 0, true},
		{"abc", 0, true},
		{"12.34.56", 0, true},
		{"电费", 0, true},
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
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("NormalizeAmount(%q) = %v, want %v", tc.raw, got, tc.want)
		}
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
