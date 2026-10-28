package core

import (
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/database"
)

func TestUnit_UT317_RetentionAvailabilityLabels(t *testing.T) {
	expiry := time.Date(2027, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 2*60*60))
	for _, tc := range []struct {
		name         string
		source       database.VideoSource
		legal        bool
		contestation bool
		want         string
	}{
		{name: "UT-317 active baseline", source: database.VideoSource{RetentionAnchor: "ACTIVE_BASELINE"}, want: "WHILE_BASELINE_ACTIVE"},
		{name: "UT-317 open contestation", source: database.VideoSource{RetentionAnchor: "ACTIVE_BASELINE"}, contestation: true, want: "CONTESTATION_HOLD"},
		{name: "UT-317 legal hold", source: database.VideoSource{RetentionAnchor: "FIXED"}, legal: true, contestation: true, want: "LEGAL_HOLD"},
		{name: "UT-317 expired frames remain", source: database.VideoSource{Status: "EXPIRED"}, want: "EXPIRED_FRAMES_PRESERVED"},
		{name: "UT-317 fixed date", source: database.VideoSource{RetentionAnchor: "FIXED", SourceExpiryAt: &expiry}, want: "2027-01-02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetentionAvailabilityLabel(tc.source, tc.legal, tc.contestation); got != tc.want {
				t.Fatalf("label=%q, want %q", got, tc.want)
			}
		})
	}
}
