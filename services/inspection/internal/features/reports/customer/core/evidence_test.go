package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func snapshotWith(evidence ...reportcore.Evidence) reportcore.Snapshot {
	return reportcore.Snapshot{Evidence: evidence}
}

func current(id, key string) reportcore.Evidence {
	return reportcore.Evidence{ID: id, RequirementKey: key, Role: "CURRENT", Availability: "AVAILABLE", DisplayDigest: strings.Repeat("a", 64)}
}

// UT-033: SIMPLE returns only the current approved items.
func TestSimpleModeReturnsCurrentItemsOnlyUT033(t *testing.T) {
	mapper := Mapper{Published: true, Prior: []reportcore.Snapshot{snapshotWith(current("old", "sala"))}}
	items, err := mapper.Map(snapshotWith(current("new", "sala")), Simple)
	if err != nil || len(items) != 1 || items[0].ID != "new" || items[0].State != StateCurrent || items[0].Historical {
		t.Fatalf("simple items = %+v (%v)", items, err)
	}
}

// UT-034 and UT-037: ADVANCED adds replaced and discarded lineage as historical
// items that keep the content hash of the version they came from.
func TestAdvancedModeAddsReplacedAndDiscardedLineageUT034UT037(t *testing.T) {
	oldReplaced := current("old-sala", "sala")
	oldReplaced.DisplayDigest = strings.Repeat("1", 64)
	oldDiscarded := current("old-quarto", "quarto")
	reference := reportcore.Evidence{ID: "ref", RequirementKey: "sala", Role: "REFERENCE"}
	mapper := Mapper{Published: true, Prior: []reportcore.Snapshot{
		snapshotWith(reference, current("mid-sala", "sala")),
		snapshotWith(oldReplaced, oldDiscarded, current("mid-sala", "sala")),
	}}
	items, err := mapper.Map(snapshotWith(current("new-sala", "sala")), Advanced)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Item{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if len(items) != 4 {
		t.Fatalf("items = %+v, want current + 2 replaced + 1 discarded", items)
	}
	if got := byID["new-sala"]; got.State != StateCurrent || got.Historical || got.LineageID != "new-sala" {
		t.Fatalf("current = %+v", got)
	}
	if got := byID["old-sala"]; got.State != StateReplaced || !got.Historical || got.ReplacedBy != "new-sala" || got.LineageID != "new-sala" || got.ContentDigest != strings.Repeat("1", 64) {
		t.Fatalf("replaced = %+v", got)
	}
	if got := byID["mid-sala"]; got.State != StateReplaced || got.ReplacedBy != "new-sala" {
		t.Fatalf("duplicate across versions must appear once as replaced: %+v", got)
	}
	if got := byID["old-quarto"]; got.State != StateDiscarded || !got.Historical || got.ReplacedBy != "" || got.LineageID != "old-quarto" {
		t.Fatalf("discarded = %+v", got)
	}
	if _, reference := byID["ref"]; reference {
		t.Fatal("origin reference evidence is not customer lineage")
	}
}

// UT-035: sensitive evidence stays as metadata and is marked blocked, whether
// flagged in the snapshot or by a later screening decision.
func TestSensitiveEvidenceIsBlockedUT035(t *testing.T) {
	flagged := current("flagged", "a")
	flagged.Flags = []string{"sensitive"}
	unavailable := current("unavailable", "d")
	unavailable.Availability = "UNAVAILABLE"
	mapper := Mapper{Published: true, Sensitive: map[string]bool{"screened": true}}
	items, err := mapper.Map(snapshotWith(flagged, current("screened", "b"), current("clear", "c"), unavailable), Simple)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"flagged": BlockedAvailability, "screened": BlockedAvailability, "clear": "AVAILABLE", "unavailable": BlockedAvailability}
	for _, item := range items {
		if item.MediaAvailability != want[item.ID] {
			t.Errorf("%s availability = %s, want %s", item.ID, item.MediaAvailability, want[item.ID])
		}
	}
}

// UT-036: an unpublished snapshot is never mapped, and the mode is validated.
func TestMapperRejectsUnpublishedSnapshotUT036(t *testing.T) {
	if _, err := (Mapper{}).Map(snapshotWith(current("a", "k")), Simple); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("unpublished error = %v, want NOT_FOUND", err)
	}
	if _, err := (Mapper{Published: true}).Map(snapshotWith(), Mode("RAW")); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("mode error = %v, want INVALID_INPUT", err)
	}
}

func storedSnapshot(t *testing.T, snapshot reportcore.Snapshot) database.ReportSnapshot {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return database.ReportSnapshot{CanonicalJSON: raw}
}

func TestReportMapperReadsLineageOnlyInAdvancedMode(t *testing.T) {
	mapper := ReportMapper{Prior: []database.ReportSnapshot{storedSnapshot(t, snapshotWith(current("old", "sala")))}}
	row := storedSnapshot(t, snapshotWith(current("new", "sala")))
	simple, err := mapper.Map(context.Background(), row, Simple)
	if err != nil || len(simple) != 1 {
		t.Fatalf("simple = %+v (%v)", simple, err)
	}
	advanced, err := mapper.Map(context.Background(), row, Advanced)
	if err != nil || len(advanced) != 2 {
		t.Fatalf("advanced = %+v (%v)", advanced, err)
	}
	if _, err := mapper.Map(context.Background(), database.ReportSnapshot{CanonicalJSON: []byte("not json")}, Simple); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("corrupt snapshot error = %v, want NOT_FOUND", err)
	}
}

type fakePresigner struct {
	err   error
	calls int
}

func (p *fakePresigner) PresignGet(_ context.Context, bucket, key string, ttl time.Duration) (string, error) {
	p.calls++
	if p.err != nil {
		return "https://partial.example/leak", p.err
	}
	return "https://signed.example/" + bucket + "/" + key + "?ttl=" + ttl.String(), nil
}

// UT-038: signing happens only for authorized, non-sensitive media, with a
// short TTL.
func TestURLIssuerSignsOnlyAfterChecksUT038(t *testing.T) {
	signer := &fakePresigner{}
	issuer := URLIssuer{Store: signer, Bucket: "bucket", TTL: time.Hour, Authorized: func(_ context.Context, id string) (string, bool, bool, error) {
		switch id {
		case "ok":
			return "object/ok", true, false, nil
		case "sensitive":
			return "object/sensitive", true, true, nil
		default:
			return "object/other", false, false, nil
		}
	}}
	url, err := issuer.Issue(context.Background(), "ok")
	if err != nil || !strings.Contains(url, "object/ok") || !strings.Contains(url, "ttl=5m0s") {
		t.Fatalf("url = %q (%v); an oversized TTL must fall back to 5 minutes", url, err)
	}
	for _, id := range []string{"sensitive", "revoked", ""} {
		if url, err := issuer.Issue(context.Background(), id); url != "" || !apperror.Is(err, apperror.NotFound) {
			t.Errorf("%q: url=%q err=%v, want NOT_FOUND without URL (UT-039)", id, url, err)
		}
	}
	if signer.calls != 1 {
		t.Fatalf("storage was asked to sign %d times, want only the allowed one", signer.calls)
	}
}

// UT-039 and UT-040: revoked access answers NOT_FOUND and a storage failure
// returns no partial URL.
func TestURLIssuerFailuresReturnNoURLUT039UT040(t *testing.T) {
	failing := &fakePresigner{err: errors.New("storage down")}
	issuer := URLIssuer{Store: failing, Bucket: "bucket", Authorized: func(context.Context, string) (string, bool, bool, error) {
		return "object/key", true, false, nil
	}}
	url, err := issuer.Issue(context.Background(), "media")
	if url != "" || !apperror.Is(err, apperror.DependencyUnavailable) {
		t.Fatalf("storage failure url=%q err=%v, want empty URL and DEPENDENCY_UNAVAILABLE", url, err)
	}
	authFailure := URLIssuer{Store: &fakePresigner{}, Authorized: func(context.Context, string) (string, bool, bool, error) {
		return "", false, false, errors.New("lookup failed")
	}}
	if url, err := authFailure.Issue(context.Background(), "media"); url != "" || err == nil {
		t.Fatalf("authorization failure url=%q err=%v", url, err)
	}
	if url, err := (URLIssuer{}).Issue(context.Background(), identity.NewID().String()); url != "" || !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("unconfigured issuer url=%q err=%v", url, err)
	}
}

// UT-055: an expired or failed media fetch leaves the approved report facts
// intact and never returns the storage provider's partial URL.
func TestUT055MediaFailureDoesNotRewriteSnapshotOrLeakURL(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Name: "Cozinha", Components: []reportcore.ChecklistComponent{{Label: "Piso", Status: "OBSERVED", Description: "Piso descrito", EvidenceIDs: []string{"media-1"}}}}}}}
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	signer := &fakePresigner{err: errors.New("storage URL https://private.example/token")}
	issuer := URLIssuer{Store: signer, Bucket: "private", Authorized: func(context.Context, string) (string, bool, bool, error) {
		return "private/object-key", true, false, nil
	}}
	url, err := issuer.Issue(context.Background(), "media-1")
	if url != "" || !apperror.Is(err, apperror.DependencyUnavailable) || strings.Contains(err.Error(), "private.example") {
		t.Fatalf("failed fetch url=%q err=%v, want safe failure without URL", url, err)
	}
	view := ProjectChecklist(snapshot)
	if view == nil || view.Rooms[0].Components[0].Description != "Piso descrito" {
		t.Fatalf("customer facts changed after failed fetch: %+v", view)
	}
	after, err := json.Marshal(snapshot)
	if err != nil || string(before) != string(after) {
		t.Fatalf("snapshot changed after failed fetch: before=%s after=%s err=%v", before, after, err)
	}
}

// UT-056: a new media URL can be minted only while authorization is current;
// URL expiry or refresh never changes the already issued PDF digest.
func TestUT056MediaRefreshRechecksAuthorizationWithoutRewritingPDF(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Advisory: "Laudo emitido", Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Name: "Sala", Components: []reportcore.ChecklistComponent{{Label: "Parede", Status: "OBSERVED", Description: "Parede registrada", EvidenceIDs: []string{"media-2"}}}}}}}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	pdfSourceDigest := sha256.Sum256(canonical)
	signer := &fakePresigner{}
	authorized := true
	issuer := URLIssuer{Store: signer, Bucket: "customer", TTL: 5 * time.Minute, Authorized: func(context.Context, string) (string, bool, bool, error) {
		return "display/object", authorized, false, nil
	}}
	first, err := issuer.Issue(context.Background(), "media-2")
	if err != nil || first == "" {
		t.Fatalf("initial media URL=%q err=%v", first, err)
	}
	authorized = false
	refreshed, err := issuer.Issue(context.Background(), "media-2")
	if refreshed != "" || !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("revoked refresh url=%q err=%v, want NOT_FOUND without URL", refreshed, err)
	}
	canonicalAfter, err := json.Marshal(snapshot)
	if err != nil || sha256.Sum256(canonicalAfter) != pdfSourceDigest || signer.calls != 1 {
		t.Fatalf("issued PDF source changed or revoked media was signed: calls=%d err=%v", signer.calls, err)
	}
}

// Every sensitive flag spelling and the recorded availability block the visual.
func TestEvidenceBlockedCoversSensitiveFlagsAndAvailability(t *testing.T) {
	blocked := []reportcore.Evidence{
		{Availability: BlockedAvailability},
		{Availability: "sensitive_blocked"},
		{Flags: []string{"SENSITIVE"}},
		{Flags: []string{"sensitive_detection"}},
		{Flags: []string{"OTHER", BlockedAvailability}},
	}
	for i, evidence := range blocked {
		if !EvidenceBlocked(evidence) {
			t.Errorf("case %d must be blocked: %+v", i, evidence)
		}
		items, err := (Mapper{Published: true}).Map(snapshotWith(evidence), Simple)
		if err != nil || len(items) != 1 || items[0].MediaAvailability != BlockedAvailability {
			t.Errorf("case %d mapped as %+v (%v), want SENSITIVE_BLOCKED", i, items, err)
		}
	}
	for _, evidence := range []reportcore.Evidence{{}, {Availability: "AVAILABLE"}, {Flags: []string{"LOW_LIGHT"}}} {
		if EvidenceBlocked(evidence) {
			t.Errorf("must not be blocked: %+v", evidence)
		}
	}
}

// Customer-facing errors keep their stable code and field in Portuguese text.
func TestCustomerEvidenceErrorsArePortuguese(t *testing.T) {
	_, err := (Mapper{}).Map(snapshotWith(), Simple)
	if !apperror.Is(err, apperror.NotFound) || err.Error() != "Relatório não encontrado." {
		t.Fatalf("unpublished error = %v", err)
	}
	_, err = (Mapper{Published: true}).Map(snapshotWith(), Mode("RAW"))
	if !apperror.Is(err, apperror.InvalidInput) || err.Error() != "Modo de evidências inválido." {
		t.Fatalf("mode error = %v", err)
	}
	_, err = (URLIssuer{}).Issue(context.Background(), "x")
	if !apperror.Is(err, apperror.NotFound) || err.Error() != "Mídia não encontrada." {
		t.Fatalf("media error = %v", err)
	}
}
