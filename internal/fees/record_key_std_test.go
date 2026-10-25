package fees_test

// record_key_std_test.go — issue #4's std-build pins: the recordKey
// recognizer aliases (order/meter/work-order), and the electricity
// double-mapping (电表编号 -> recordKey AND meterNo).
import (
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/fees"
)

func TestRecordKeyRecognition(t *testing.T) {
	cases := []struct {
		headers []string
		field   string
		want    string
	}{
		{[]string{"站址编码", "订单号", "塔类费用(元)", "账期"}, "recordKey", "订单号"},
		{[]string{"站址编码", "需求确认单编号", "金额(元)", "账期"}, "recordKey", "需求确认单编号"},
		{[]string{"站址编码", "工单号", "修理费(元)", "账期"}, "recordKey", "工单号"},
		{[]string{"站址编码", "电表编号", "电费(元)", "账期"}, "recordKey", "电表编号"},
	}
	for _, c := range cases {
		cols := fees.RecognizeLedgerFields(c.headers)
		if cols[c.field] != c.want {
			t.Errorf("headers %v: %s = %q, want %q", c.headers, c.field, cols[c.field], c.want)
		}
	}
	// 电表编号 maps BOTH identities: the meter is the record.
	cols := fees.RecognizeLedgerFields([]string{"电表编号", "电费(元)"})
	if cols["meterNo"] != "电表编号" || cols["recordKey"] != "电表编号" {
		t.Fatalf("meter double-mapping broken: %v", cols)
	}
}
