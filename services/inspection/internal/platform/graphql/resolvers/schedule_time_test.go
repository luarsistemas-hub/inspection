package resolvers

import (
	"errors"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/apperror"
)

func TestParseScheduleStart(t *testing.T) {
	tests := []struct {
		name, value, timezone, want, field string
	}{
		{name: "selected timezone", value: "2026-09-25T23:32", timezone: "Africa/Accra", want: "2026-09-25T23:32:00Z"},
		{name: "nonzero offset", value: "2026-09-25T23:32", timezone: "America/Sao_Paulo", want: "2026-09-26T02:32:00Z"},
		{name: "existing RFC3339 client", value: "2026-09-25T23:32:00-03:00", timezone: "America/Sao_Paulo", want: "2026-09-26T02:32:00Z"},
		{name: "spring DST gap", value: "2026-03-08T02:30", timezone: "America/New_York", field: "startsAt"},
		{name: "invalid timezone", value: "2026-09-25T23:32", timezone: "Invalid/Timezone", field: "timezone"},
		{name: "invalid date", value: "not-a-date", timezone: "Africa/Accra", field: "startsAt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScheduleStart(tt.value, tt.timezone)
			if tt.field != "" {
				var inputError *apperror.Error
				if !errors.As(err, &inputError) || inputError.Field != tt.field {
					t.Fatalf("error = %v, want invalid field %q", err, tt.field)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Format(time.RFC3339) != tt.want {
				t.Fatalf("start = %s, want %s", got.Format(time.RFC3339), tt.want)
			}
		})
	}
}
