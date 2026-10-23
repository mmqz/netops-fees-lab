package domain

// Closed-enum pins for the parser/validator pairs the fresh full-suite
// coverage profile flagged as zero-covered: RunSource, WebhookDeliveryStatus,
// ClueDecisionStatus, and the metrics trio (MetricValueKind, MetricDirection,
// PointQuality). Each follows the same house pattern — IsValid answers the
// closed set, Parse converts with a typed error carrying the accepted
// vocabulary — and each guards an API boundary: a status string that slips
// past validation today becomes a garbage row tomorrow.

import (
	"testing"
)

func TestRunSourceClosedEnum(t *testing.T) {
	for _, s := range []RunSource{RunSourceAPI, RunSourceCLI, RunSourceScheduler} {
		if !s.IsValid() {
			t.Errorf("RunSource(%q).IsValid() = false", s)
		}
		if got, err := ParseRunSource(string(s)); err != nil || got != s {
			t.Errorf("ParseRunSource(%q) = %v, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "API", "api ", "web", "cron"} {
		if v := RunSource(bad); v.IsValid() {
			t.Errorf("RunSource(%q).IsValid() = true, want false (closed set)", bad)
		}
		if _, err := ParseRunSource(bad); err == nil {
			t.Errorf("ParseRunSource(%q) err = nil, want the vocabulary error", bad)
		}
	}
}

func TestWebhookDeliveryStatusClosedEnum(t *testing.T) {
	for _, s := range []WebhookDeliveryStatus{WebhookDelivered, WebhookFailed} {
		if !s.IsValid() {
			t.Errorf("WebhookDeliveryStatus(%q).IsValid() = false", s)
		}
		if got, err := ParseWebhookDeliveryStatus(string(s)); err != nil || got != s {
			t.Errorf("ParseWebhookDeliveryStatus(%q) = %v, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "Delivered", "pending", "ok"} {
		if v := WebhookDeliveryStatus(bad); v.IsValid() {
			t.Errorf("WebhookDeliveryStatus(%q).IsValid() = true, want false", bad)
		}
		if _, err := ParseWebhookDeliveryStatus(bad); err == nil {
			t.Errorf("ParseWebhookDeliveryStatus(%q) err = nil, want the vocabulary error", bad)
		}
	}
}

func TestClueDecisionStatusClosedEnum(t *testing.T) {
	for _, s := range []ClueDecisionStatus{ClueDecisionVerified, ClueDecisionDismissed} {
		if !s.IsValid() {
			t.Errorf("ClueDecisionStatus(%q).IsValid() = false", s)
		}
		if got, err := ParseClueDecisionStatus(string(s)); err != nil || got != s {
			t.Errorf("ParseClueDecisionStatus(%q) = %v, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "verified ", "confirmed", "rejected"} {
		if v := ClueDecisionStatus(bad); v.IsValid() {
			t.Errorf("ClueDecisionStatus(%q).IsValid() = true, want false", bad)
		}
		if _, err := ParseClueDecisionStatus(bad); err == nil {
			t.Errorf("ParseClueDecisionStatus(%q) err = nil, want the vocabulary error", bad)
		}
	}
}

func TestMetricsEnumsClosedSets(t *testing.T) {
	for _, k := range []MetricValueKind{MetricValueKindGauge, MetricValueKindCounter, MetricValueKindRatio} {
		if !k.IsValid() {
			t.Errorf("MetricValueKind(%q).IsValid() = false", k)
		}
	}
	for _, d := range []MetricDirection{MetricDirectionHigherIsBetter, MetricDirectionLowerIsBetter, MetricDirectionNeutral} {
		if !d.IsValid() {
			t.Errorf("MetricDirection(%q).IsValid() = false", d)
		}
	}
	for _, q := range []PointQuality{PointQualityOk, PointQualityMissing, PointQualitySuspect} {
		if !q.IsValid() {
			t.Errorf("PointQuality(%q).IsValid() = false", q)
		}
	}
	for _, bad := range []MetricValueKind{"", "Gauge", "rate"} {
		if bad.IsValid() {
			t.Errorf("MetricValueKind(%q).IsValid() = true, want false", bad)
		}
	}
	for _, bad := range []MetricDirection{"", "higher", "unknown"} {
		if bad.IsValid() {
			t.Errorf("MetricDirection(%q).IsValid() = true, want false", bad)
		}
	}
	for _, bad := range []PointQuality{"", "OK", "good"} {
		if bad.IsValid() {
			t.Errorf("PointQuality(%q).IsValid() = true, want false", bad)
		}
	}
}
