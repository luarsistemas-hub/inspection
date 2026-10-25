package core

import (
	"sort"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/apperror"
)

func TestUnit_UT289_BlankStatement(t *testing.T) {
	for _, value := range []string{"", " \t\n "} {
		if _, err := validateStatement(value); !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("statement %q: want invalid input, got %v", value, err)
		}
	}
}

func TestUnit_UT290_StatementLengthBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "UT-290 accepts 4000 characters", value: strings.Repeat("a", 4000), valid: true},
		{name: "UT-290 rejects 4001 characters", value: strings.Repeat("a", 4001)},
		{name: "UT-290 counts Unicode characters", value: strings.Repeat("é", 4000), valid: true},
		{name: "UT-290 accepts multiline statement and tabs", value: "line one\nline two\tindented", valid: true},
		{name: "UT-290 rejects NUL", value: "statement\x00invalid"},
		{name: "UT-290 rejects other controls", value: "statement\x01invalid"},
		{name: "UT-290 rejects invalid UTF-8", value: string([]byte{0xff})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateStatement(tc.value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

func TestUnit_UT297_ResolutionReasonAllowsNewlinesAndRejectsControls(t *testing.T) {
	if !validResolutionReason("first line\nsecond line\tindented") {
		t.Fatal("UT-297 multiline resolution reason should be allowed")
	}
	for _, reason := range []string{"bad\x00text", "bad\x01text", string([]byte{0xff})} {
		if validResolutionReason(reason) {
			t.Fatalf("UT-297 invalid resolution reason %q was accepted", reason)
		}
	}
}

func TestUnit_UT291_DeadlineBoundary(t *testing.T) {
	deadline := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		now  time.Time
		open bool
	}{
		{name: "UT-291 exact deadline is closed", now: deadline, open: false},
		{name: "UT-291 one millisecond before is open", now: deadline.Add(-time.Millisecond), open: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deadlineOpen(tc.now, deadline); got != tc.open {
				t.Fatalf("deadlineOpen()=%v, want %v", got, tc.open)
			}
		})
	}
}

func TestUnit_UT302_BaselineDeadlinePolicyRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		days  int
		valid bool
	}{
		{name: "UT-302 rejects zero", days: 0},
		{name: "UT-302 accepts one", days: 1, valid: true},
		{name: "UT-302 accepts ninety", days: 90, valid: true},
		{name: "UT-302 rejects ninety one", days: 91},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDeadlineDays(tc.days)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

func TestUnit_UT306_BaselineAcknowledgmentTemplate(t *testing.T) {
	template, err := core.DefaultCatalog().Resolve(core.TemplateRef{Name: "baseline-acknowledgment", Version: "v1"}, core.ChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	required := append([]string(nil), template.Required...)
	sort.Strings(required)
	want := []string{"assetName", "deadlineDate", "link", "purposeLabel"}
	if strings.Join(required, ",") != strings.Join(want, ",") {
		t.Fatalf("UT-306 required variables=%v, want %v", template.Required, want)
	}
	variables := map[string]string{"assetName": "Apartamento 4B", "purposeLabel": "entrada", "deadlineDate": "30/10/2026 12:00", "link": "https://capture.test/baseline/token"}
	rendered, err := template.Render(variables)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range variables {
		if !strings.Contains(rendered.Text, value) {
			t.Errorf("UT-306 rendered text does not include %q: %q", value, rendered.Text)
		}
	}
	if rendered.HTML == "" {
		t.Fatal("UT-306 template has no HTML body")
	}
	for _, missing := range want {
		partial := map[string]string{}
		for key, value := range variables {
			if key != missing {
				partial[key] = value
			}
		}
		if _, err := template.Render(partial); err == nil {
			t.Errorf("UT-306 template rendered without %q", missing)
		}
	}
	for _, channel := range []core.Channel{core.ChannelSMS, core.ChannelWhatsApp} {
		if _, err := core.DefaultCatalog().Resolve(core.TemplateRef{Name: "baseline-acknowledgment", Version: "v1"}, channel); err != nil {
			t.Errorf("UT-306 template unavailable on %s: %v", channel, err)
		}
	}
}
