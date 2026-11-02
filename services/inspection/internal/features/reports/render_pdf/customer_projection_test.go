package render_pdf

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
)

// A blocked video frame becomes a placeholder in the customer copy: no image is
// requested for it and neither the caption nor the link carries its identifiers.
func TestCustomerProjectionRedactsBlockedVideoFrames(t *testing.T) {
	frameID, sourceID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	pts, ms := int64(7_000_000), int64(7_000)
	frame := func(id identity.ID, mutate func(*reportcore.Evidence)) reportcore.Evidence {
		item := reportcore.Evidence{ID: id.String(), RequirementKey: "video:sink", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: id.String(), SourceMediaID: sourceID.String(), PreparationID: preparationID.String(), FrameDigest: strings.Repeat("c", 64), DisplayDigest: strings.Repeat("c", 64), Availability: "AVAILABLE", PTSMicros: &pts, TimestampMS: &ms}
		if mutate != nil {
			mutate(&item)
		}
		return item
	}
	clean := identity.NewID()
	snapshot := validPDFSnapshot()
	snapshot.Evidence = []reportcore.Evidence{
		frame(frameID, func(e *reportcore.Evidence) { e.Flags = []string{"SENSITIVE_DETECTION"} }),
		frame(identity.NewID(), func(e *reportcore.Evidence) { e.Availability = "SENSITIVE_BLOCKED" }),
		frame(clean, nil),
	}
	original := snapshot.Evidence[0]
	projected, _, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Evidence[0].FrameID != original.FrameID || snapshot.Evidence[0].Availability != original.Availability {
		t.Fatal("the projection mutated the source snapshot")
	}
	for _, item := range projected.Evidence[:2] {
		_, caption, link, err := evidenceComponents(item, nil, "Pia", "snapshot-1", "https://dashboard.example")
		if err != nil || link != "" || item.Availability != "MISSING" || strings.Contains(caption, item.ID) || strings.Contains(caption, sourceID.String()) || strings.Contains(caption, "Frame") {
			t.Fatalf("blocked frame leaked: caption=%q link=%q item=%+v err=%v", caption, link, item, err)
		}
	}
	if _, _, link, _ := evidenceComponents(projected.Evidence[2], nil, "Pia", "snapshot-1", "https://dashboard.example"); !strings.Contains(link, clean.String()) {
		t.Fatalf("a clean frame must keep its deep link, got %q", link)
	}
	// The internal PDF is untouched.
	internal, _, err := projectForAudience(snapshot, "PDF")
	if err != nil || internal.Evidence[0].FrameID != original.FrameID || internal.Evidence[0].Availability != "AVAILABLE" {
		t.Fatalf("internal projection changed evidence: %+v (%v)", internal.Evidence[0], err)
	}
	env := newTestEnv(t)
	reads := &keyLog{}
	env.client.getHook = reads.add
	if _, err := env.executor.loadDisplayAssets(context.Background(), env.tenantID, projected); err == nil || len(reads.keys()) > 1 {
		// Only the clean frame may be looked up; it is absent here, so integrity fails.
		t.Fatalf("blocked frames were read from the store: keys=%v err=%v", reads.keys(), err)
	}
}

type keyLog struct {
	mu   sync.Mutex
	seen []string
}

func (k *keyLog) add(key string) { k.mu.Lock(); k.seen = append(k.seen, key); k.mu.Unlock() }
func (k *keyLog) keys() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.seen...)
}

// The customer PDF embeds only photos that are neither flagged in the snapshot
// nor blocked, nor undecided, by screening at render time; the internal PDF
// still embeds all of them.
func TestCustomerPDFOmitsBlockedAndUnscreenedPhotos(t *testing.T) {
	env := newTestEnv(t)
	if err := env.db.Exec(`CREATE TABLE media.screening_runs (id blob PRIMARY KEY, tenant_id blob, media_id blob, status text, model_digest text, regions blob, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	type photo struct {
		id         identity.ID
		key        string
		decision   []string // screening runs, oldest first
		flag       bool
		wantInside bool
	}
	photos := map[string]*photo{
		"cleared":          {decision: []string{"CLEARED"}, wantInside: true},
		"overridden":       {decision: []string{"BLOCKED", "OVERRIDDEN"}, wantInside: true},
		"disabled":         {decision: []string{"DISABLED"}, wantInside: true},
		"blocked later":    {decision: []string{"CLEARED", "BLOCKED"}},
		"never screened":   {},
		"flagged snapshot": {decision: []string{"CLEARED"}, flag: true},
	}
	value := validPDFSnapshot()
	base := env.clock.Add(-time.Hour)
	for name, p := range photos {
		p.id = identity.NewID()
		data := jpegBytes(t, 40, 30)
		key, hash := objectstore.DerivativeKey(env.tenantID, p.id, "DISPLAY", data)
		p.key = key
		env.client.objects[key] = data
		if err := env.db.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: env.tenantID, MediaID: p.id, ObjectKey: key, Kind: "DISPLAY", SHA256: hash, ContentType: "image/jpeg", Width: 40, Height: 30, SizeBytes: int64(len(data))}).Error; err != nil {
			t.Fatal(err)
		}
		for i, status := range p.decision {
			if err := env.db.Exec("INSERT INTO media.screening_runs (id,tenant_id,media_id,status,model_digest,regions,created_at) VALUES (?,?,?,?,?,?,?)", identity.NewID(), env.tenantID, p.id, status, "digest", "[]", base.Add(time.Duration(i)*time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
		}
		item := reportcore.Evidence{ID: p.id.String(), RequirementKey: "roof", Role: "CURRENT", Description: name, DisplayDigest: hash, Availability: "AVAILABLE"}
		if p.flag {
			item.Flags = []string{"SENSITIVE"}
		}
		value.Evidence = append(value.Evidence, item)
	}
	canonical, digest, err := reportcore.CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := database.ReportSnapshot{ID: identity.NewID(), TenantID: env.tenantID, InspectionID: env.snapshot.InspectionID, VersionNumber: 2, PDFRenderVersion: 1, JSONDigest: digest, CanonicalJSON: json.RawMessage(canonical), HTML: json.RawMessage(`""`), CreatedAt: *env.clock}
	if err := env.db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	env.snapshot = snapshot
	reads := &keyLog{}
	env.client.getHook = reads.add
	run := func(audience string) map[string]bool {
		t.Helper()
		before := len(reads.keys())
		env.addJob(t, audience, "QUEUED", 0)
		if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
			t.Fatalf("%s worked=%v err=%v", audience, worked, err)
		}
		read := map[string]bool{}
		for _, key := range reads.keys()[before:] {
			read[key] = true
		}
		return read
	}
	customer := run("PDF_CUSTOMER")
	internal := run("PDF")
	for name, p := range photos {
		if customer[p.key] != p.wantInside {
			t.Errorf("customer PDF read the image of %q = %v, want %v", name, customer[p.key], p.wantInside)
		}
		if !internal[p.key] {
			t.Errorf("internal PDF must keep the image of %q", name)
		}
	}
}
