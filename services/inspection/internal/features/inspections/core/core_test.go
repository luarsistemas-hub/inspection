package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
)

func TestChecklistOnlyRejectsTemplateWithMandatoryFixedOrigin(t *testing.T) {
	err := validateComparisonMode(catalog.TemplateDocument{DefaultComparisonMode: catalog.FixedOrigin}, catalog.ChecklistOnly)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput {
		t.Fatalf("validation error = %v, want INVALID_INPUT", err)
	}
}

func TestInspectionRejectsV2TemplateUntilEnvironmentCaptureTask03(t *testing.T) {
	if err := validateTemplateCaptureVersion(catalog.TemplateDocument{SchemaVersion: 1}); err != nil {
		t.Fatalf("legacy template rejected: %v", err)
	}
	err := validateTemplateCaptureVersion(catalog.TemplateDocument{SchemaVersion: 2})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("schema v2 error=%v, want explicit INVALID_STATE", err)
	}
}

func TestInspectionCaptureRequirementsUseSingleVideoSlotAndPreservePhotoRequirements(t *testing.T) {
	video := inspectionCaptureRequirements("VIDEO", "FIXED_ORIGIN", []capturecore.Requirement{{Key: "video:" + identity.NewID().String(), EvidenceKind: "VIDEO_FRAME"}})
	if len(video) != 1 || video[0].Key != "video:main" || video[0].EvidenceKind != "VIDEO" || video[0].MinimumMedia != 1 || video[0].MaximumMedia != 1 || video[0].ComparisonTarget != "FIXED_ORIGIN" {
		t.Fatalf("video capture requirements = %+v", video)
	}
	for _, mode := range []string{"CHECKLIST_ONLY", "CURRENT_ONLY"} {
		got := inspectionCaptureRequirements("VIDEO", mode, []capturecore.Requirement{{Key: "room", EvidenceKind: "VIDEO"}})
		if len(got) != 1 || got[0].Key != "video:main" || got[0].ComparisonTarget != mode {
			t.Fatalf("%s video requirements = %+v", mode, got)
		}
	}
	photo := []capturecore.Requirement{{Key: "origin:" + identity.NewID().String(), EvidenceKind: "PHOTO"}}
	gotPhoto := inspectionCaptureRequirements("PHOTO", "FIXED_ORIGIN", photo)
	if len(gotPhoto) != 1 || gotPhoto[0] != photo[0] {
		t.Fatalf("photo capture requirements changed: got=%+v want=%+v", gotPhoto, photo)
	}
}

func TestUT022UT023InspectionStateMachine(t *testing.T) {
	valid := [][2]string{{"PLANNED", "INVITED"}, {"INVITED", "IN_PROGRESS"}, {"IN_PROGRESS", "SUBMITTED"}, {"SUBMITTED", "ANALYZING"}, {"ANALYZING", "RECAPTURE_PENDING"}, {"RECAPTURE_PENDING", "ANALYZING"}, {"ANALYZING", "COMPLETED"}}
	for _, transition := range valid {
		if err := ValidateInspectionTransition(transition[0], transition[1], 0, ""); err != nil {
			t.Fatalf("%v: %v", transition, err)
		}
	}
	for _, transition := range [][2]string{{"PLANNED", "COMPLETED"}, {"COMPLETED", "PLANNED"}, {"CANCELED", "CANCELED"}, {"INVALIDATED", "INVALIDATED"}} {
		if ValidateInspectionTransition(transition[0], transition[1], 0, "") == nil {
			t.Fatalf("invalid transition accepted: %v", transition)
		}
	}
	if err := ValidateInspectionTransition("IN_PROGRESS", "CANCELED", 1, ""); err == nil {
		t.Fatal("cancellation after evidence accepted")
	}
	if err := ValidateInspectionTransition("SUBMITTED", "INVALIDATED", 1, "reason"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInspectionTransition("SUBMITTED", "INVALIDATED", 1, ""); err == nil {
		t.Fatal("blank invalidation reason accepted")
	}
}

func TestAttentionInspectionTransitions(t *testing.T) {
	allowed := [][2]string{
		{StatusAnalyzing, StatusReviewRequired}, {StatusAnalyzing, StatusProcessingFailed},
		{StatusReviewRequired, StatusAnalyzing}, {StatusReviewRequired, StatusReplacementPending},
		{StatusProcessingFailed, StatusAnalyzing}, {StatusProcessingFailed, StatusReplacementPending},
		{StatusReplacementPending, StatusAnalyzing}, {StatusReplacementPending, StatusReviewRequired},
	}
	for _, transition := range allowed {
		if err := ValidateInspectionTransition(transition[0], transition[1], 1, ""); err != nil {
			t.Errorf("transition %s -> %s rejected: %v", transition[0], transition[1], err)
		}
	}
	for _, transition := range [][2]string{{StatusPlanned, StatusReviewRequired}, {StatusReplacementPending, StatusProcessingFailed}, {StatusReviewRequired, StatusCompleted}} {
		if err := ValidateInspectionTransition(transition[0], transition[1], 1, ""); err == nil {
			t.Errorf("transition %s -> %s unexpectedly allowed", transition[0], transition[1])
		}
	}
	if got := statusesForGroup("ATTENTION"); len(got) != 3 {
		t.Fatalf("attention group statuses = %v, want 3 states", got)
	}
}

func TestInspectionListFiltersAndSearchEscaping(t *testing.T) {
	if !validInspectionStatusGroup("") || !validInspectionStatusGroup("COMPLETED") || validInspectionStatusGroup("UNKNOWN") {
		t.Fatal("inspection status group validation returned an unexpected result")
	}
	if got := statusesForGroup("PLANNING"); strings.Join(got, ",") != "PLANNED,INVITED" {
		t.Fatalf("planning statuses = %v", got)
	}
	if got := statusesForGroup("EXECUTION"); strings.Join(got, ",") != "IN_PROGRESS,SUBMITTED,ANALYZING,RECAPTURE_PENDING" {
		t.Fatalf("execution statuses = %v", got)
	}
	if got := statusesForGroup("COMPLETED"); strings.Join(got, ",") != "COMPLETED" {
		t.Fatalf("completed statuses = %v", got)
	}
	if got := statusesForGroup("CLOSED"); strings.Join(got, ",") != "CANCELED,INVALIDATED" {
		t.Fatalf("closed statuses = %v", got)
	}
	if got := escapeInspectionSearch(`Elvio%_\\`); got != `Elvio\%\_\\\\` {
		t.Fatalf("escaped search = %q", got)
	}
}

func TestIT121ToIT140ReminderAndLifecycleBoundaries(t *testing.T) {
	due := time.Now().UTC().Truncate(time.Second)
	deadline := due.Add(time.Hour)
	values, err := NormalizeReminders([]time.Time{due.Add(10 * time.Minute), due.Add(10 * time.Minute), due.Add(20 * time.Minute)}, due, deadline)
	if err != nil || len(values) != 2 {
		t.Fatalf("reminder normalization: %v %v", values, err)
	}
	for _, values := range [][]time.Time{{due.Add(-time.Second)}, {deadline.Add(time.Second)}, {due, due.Add(time.Minute), due.Add(2 * time.Minute), due.Add(3 * time.Minute)}} {
		if _, err := NormalizeReminders(values, due, deadline); err == nil {
			t.Fatalf("invalid reminders accepted: %v", values)
		}
	}
	if _, err := (Service{}).Invalidate(context.Background(), identity.NewID(), identity.NewID(), 1, strings.Repeat("a", 2001)); err == nil {
		t.Fatal("oversized lifecycle reason accepted")
	}
}
