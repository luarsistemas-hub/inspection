package configure_policy

import (
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
)

func TestValidateRejectsEveryDisallowedPeriod(t *testing.T) {
	tenant := identity.NewID()
	tests := []struct {
		name  string
		input Input
		field string
	}{
		{"missing tenant", Input{EvidenceDays: 1825, OperationalDays: 365}, "tenantId"},
		{"zero evidence", Input{TenantID: tenant, EvidenceDays: 0, OperationalDays: 365}, "evidenceDays"},
		{"negative evidence", Input{TenantID: tenant, EvidenceDays: -1, OperationalDays: 365}, "evidenceDays"},
		{"evidence overflows durations", Input{TenantID: tenant, EvidenceDays: MaxDays + 1, OperationalDays: 365}, "evidenceDays"},
		{"zero operational", Input{TenantID: tenant, EvidenceDays: 1825}, "operationalDays"},
		{"negative security", Input{TenantID: tenant, EvidenceDays: 1825, OperationalDays: 365, SecurityDays: -1}, "securityDays"},
		{"security above the ceiling", Input{TenantID: tenant, EvidenceDays: 1825, OperationalDays: 365, SecurityDays: MaxDays + 1}, "securityDays"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.input)
			if !apperror.Is(err, apperror.InvalidInput) {
				t.Fatalf("Validate() = %v, want INVALID_INPUT", err)
			}
			if _, field, _ := apperror.Public(err); field != tc.field {
				t.Fatalf("field = %q, want %q", field, tc.field)
			}
		})
	}
}

func TestValidateAcceptsBoundaries(t *testing.T) {
	tenant := identity.NewID()
	for _, input := range []Input{
		{TenantID: tenant, EvidenceDays: MinDays, OperationalDays: MinDays},
		{TenantID: tenant, EvidenceDays: MaxDays, OperationalDays: MaxDays, SecurityDays: MaxDays},
		{TenantID: tenant, EvidenceDays: 1825, OperationalDays: 365, SecurityDays: 0},
	} {
		if err := Validate(input); err != nil {
			t.Fatalf("Validate(%+v) = %v", input, err)
		}
	}
}

func TestMaxDaysKeepsPurgeDurationsWithinRange(t *testing.T) {
	// The purge worker computes days*24h as a time.Duration; the ceiling must
	// never overflow it into a negative period.
	if d := time.Duration(MaxDays) * 24 * time.Hour; d <= 0 {
		t.Fatalf("MaxDays=%d overflows time.Duration", MaxDays)
	}
	overflowing := 200000
	if d := time.Duration(overflowing) * 24 * time.Hour; d > 0 {
		t.Fatal("the overflow example no longer overflows; update the integration case")
	}
}
