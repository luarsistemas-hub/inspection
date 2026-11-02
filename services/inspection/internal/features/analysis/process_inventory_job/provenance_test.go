package process_inventory_job

import (
	"sort"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
)

// sourceKinds summarizes an item's sources as "kind:attribute" counts.
func sourceKinds(t *testing.T, seed *inventorytest.Seed, item identity.ID) map[string]int {
	t.Helper()
	var rows []database.InventoryItemAttributeSource
	if err := seed.DB.Where("item_id=?", item).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, row := range rows {
		out[row.SourceKind+":"+row.AttributeName]++
	}
	return out
}

func totalSources(t *testing.T, seed *inventorytest.Seed) int64 {
	t.Helper()
	var count int64
	if err := seed.DB.Model(&database.InventoryItemAttributeSource{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCatalogWritesPerAttributeProvenance(t *testing.T) {
	t.Run("IT-167 partly visible item has visual identity and a spoken quantity", func(t *testing.T) {
		f := newVoiceFlow(t)
		note, revision := f.note(t, "cinco pratos")
		f.seal(t)
		photo := f.env.photos[0].String()
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t, voiceItem{name: "Pratos", quantity: intp(5), evidence: []string{photo}, citations: []map[string]any{cite(note, revision, "name", "quantity")}}), nil
		})
		item := itemsOf(t, f.h.seed, f.env.env.ID)[0]
		kinds := sourceKinds(t, f.h.seed, item.ID)
		want := map[string]int{"VOICE:name": 1, "VOICE:quantity": 1, "VISUAL:name": 1, "VISUAL:material": 1, "VISUAL:color": 1, "VISUAL:condition": 1}
		if len(kinds) != len(want) {
			t.Fatalf("sources = %v, want %v", kinds, want)
		}
		for key, count := range want {
			if kinds[key] != count {
				t.Fatalf("sources = %v, want %v", kinds, want)
			}
		}
		if kinds["VISUAL:quantity"] != 0 {
			t.Fatal("a spoken quantity must not be presented as visually supported")
		}
		var voice database.InventoryItemAttributeSource
		if err := f.h.seed.DB.Where("item_id=? AND source_kind='VOICE' AND attribute_name='quantity'", item.ID).Take(&voice).Error; err != nil {
			t.Fatal(err)
		}
		if voice.NoteID == nil || *voice.NoteID != note || voice.RevisionID == nil || *voice.RevisionID != revision || voice.VisualEvidenceID != nil || voice.OriginVersionID != item.OriginVersionID {
			t.Fatalf("voice source = %+v, want the exact N1/R1 citation", voice)
		}
		evidence := evidenceOf(t, f.h.seed, item.ID)
		var visual database.InventoryItemAttributeSource
		if err := f.h.seed.DB.Where("item_id=? AND source_kind='VISUAL' AND attribute_name='name'", item.ID).Take(&visual).Error; err != nil {
			t.Fatal(err)
		}
		if visual.VisualEvidenceID == nil || *visual.VisualEvidenceID != evidence[0].ID || visual.NoteID != nil {
			t.Fatalf("visual source = %+v, want it to cite the item's own evidence row", visual)
		}
	})
	t.Run("IT-168 an item supported only by speech has no visual provenance", func(t *testing.T) {
		f := newVoiceFlow(t)
		note, revision := f.note(t, "cinco pratos no armário")
		f.seal(t)
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t, voiceItem{name: "Pratos", quantity: intp(5), citations: []map[string]any{cite(note, revision, "name", "quantity")}}), nil
		})
		item := itemsOf(t, f.h.seed, f.env.env.ID)[0]
		kinds := sourceKinds(t, f.h.seed, item.ID)
		if len(kinds) != 2 || kinds["VOICE:name"] != 1 || kinds["VOICE:quantity"] != 1 {
			t.Fatalf("sources = %v, want voice name and quantity only (frames near an interval are never proof)", kinds)
		}
	})
	t.Run("IT-172 a visual item without a spoken source shows visual provenance only", func(t *testing.T) {
		f := newVoiceFlow(t)
		f.note(t, "cinco pratos")
		f.seal(t)
		photo := f.env.photos[0].String()
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t, voiceItem{name: "Mesa", evidence: []string{photo}}), nil
		})
		item := itemsOf(t, f.h.seed, f.env.env.ID)[0]
		for key := range sourceKinds(t, f.h.seed, item.ID) {
			if !strings.HasPrefix(key, "VISUAL:") {
				t.Fatalf("unexpected non-visual source %s", key)
			}
		}
		if len(sourceKinds(t, f.h.seed, item.ID)) == 0 {
			t.Fatal("a visual item must record its visual provenance")
		}
	})
	t.Run("IT-171 an unknown note or revision records no provenance", func(t *testing.T) {
		f := newVoiceFlow(t)
		note, _ := f.note(t, "cinco pratos")
		f.seal(t)
		photo := f.env.photos[0].String()
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t,
				voiceItem{name: "Revisão inventada", evidence: []string{photo}, citations: []map[string]any{cite(note, identity.NewID(), "quantity")}},
				voiceItem{name: "Nota inventada", evidence: []string{photo}, citations: []map[string]any{cite(identity.NewID(), identity.NewID(), "quantity")}},
			), nil
		})
		for _, item := range itemsOf(t, f.h.seed, f.env.env.ID) {
			if kinds := sourceKinds(t, f.h.seed, item.ID); kinds["VOICE:quantity"] != 0 || item.Decision != "PENDING" {
				t.Fatalf("%s: sources=%v decision=%s", item.Name, kinds, item.Decision)
			}
		}
	})
	t.Run("IT-173 IT-177 many notes are all cited and one note is never counted twice", func(t *testing.T) {
		f := newVoiceFlow(t)
		first, firstRevision := f.note(t, "dois pratos")
		second, secondRevision := f.note(t, "mais três pratos")
		f.seal(t)
		photo := f.env.photos[0].String()
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t, voiceItem{name: "Pratos", quantity: intp(5), evidence: []string{photo}, citations: []map[string]any{
				cite(first, firstRevision, "name", "quantity"),
				cite(second, secondRevision, "quantity"),
				cite(first, firstRevision, "quantity"), // the same note cited twice
			}}), nil
		})
		item := itemsOf(t, f.h.seed, f.env.env.ID)[0]
		var rows []database.InventoryItemAttributeSource
		if err := f.h.seed.DB.Where("item_id=? AND source_kind='VOICE' AND attribute_name='quantity'", item.ID).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		cited := []string{}
		for _, row := range rows {
			cited = append(cited, row.NoteID.String())
		}
		sort.Strings(cited)
		want := []string{first.String(), second.String()}
		sort.Strings(want)
		if len(cited) != 2 || cited[0] != want[0] || cited[1] != want[1] {
			t.Fatalf("quantity is cited by %v, want each of the two notes exactly once", cited)
		}
	})
}

func TestCatalogReplayReplacesSourcesWithoutOrphans(t *testing.T) {
	f := newVoiceFlow(t)
	note, revision := f.note(t, "cinco pratos")
	f.seal(t)
	photo := f.env.photos[0].String()
	respond := func(llm.StructuredRequest) (string, error) {
		return voiceCatalogJSON(t, voiceItem{name: "Pratos", quantity: intp(5), evidence: []string{photo}, citations: []map[string]any{cite(note, revision, "name", "quantity")}}), nil
	}
	f.run(t, respond)
	first := totalSources(t, f.h.seed)
	f.run(t, respond)
	if again := totalSources(t, f.h.seed); again != first || first == 0 {
		t.Fatalf("sources after a replay = %d, before = %d: a retry must replace, not duplicate", again, first)
	}
	var orphans int64
	if err := f.h.seed.DB.Raw("SELECT count(*) FROM inventory.item_attribute_sources s WHERE NOT EXISTS (SELECT 1 FROM origins.inventory_items i WHERE i.id=s.item_id)").Scan(&orphans).Error; err != nil || orphans != 0 {
		t.Fatalf("orphan sources = %d err=%v", orphans, err)
	}
}

func TestCatalogNeverReplacesOrDuplicatesWhatAPersonDecided(t *testing.T) {
	f := newVoiceFlow(t)
	note, revision := f.note(t, "cinco pratos e quatro copos")
	f.seal(t)
	photo := f.env.photos[0].String()
	respond := func(llm.StructuredRequest) (string, error) {
		return voiceCatalogJSON(t,
			voiceItem{name: "Pratos", quantity: intp(5), evidence: []string{photo}, citations: []map[string]any{cite(note, revision, "name", "quantity")}},
			voiceItem{name: "Copos", quantity: intp(4), evidence: []string{photo}, citations: []map[string]any{cite(note, revision, "name", "quantity")}},
			voiceItem{name: "Talheres", evidence: []string{photo}},
		), nil
	}
	f.run(t, respond)
	var decided, rejected database.InventoryItem
	if err := f.h.seed.DB.Where("environment_id=? AND name='Pratos'", f.env.env.ID).Take(&decided).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.h.seed.DB.Where("environment_id=? AND name='Copos'", f.env.env.ID).Take(&rejected).Error; err != nil {
		t.Fatal(err)
	}
	reviewer := identity.NewID()
	now := time.Now().UTC()
	// A person accepts "Pratos" (reviewing its voice attributes) and rejects "Copos".
	if err := f.h.seed.DB.Model(&decided).Updates(map[string]any{"decision": "ACCEPTED", "version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.h.seed.DB.Model(&rejected).Updates(map[string]any{"decision": "REJECTED", "version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	for _, attribute := range []string{"name", "quantity"} {
		review := reviewer
		if err := f.h.seed.DB.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: decided.TenantID, ItemID: decided.ID, OriginVersionID: decided.OriginVersionID, AttributeName: attribute, SourceKind: "HUMAN_REVIEW", ReviewedBy: &review, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	before := sourceKinds(t, f.h.seed, decided.ID)

	// Regeneration (a replay of the room's catalog write): the decided items stay
	// as recorded and are not proposed again; only "Talheres" is refreshed.
	f.run(t, respond)

	items := itemsOf(t, f.h.seed, f.env.env.ID)
	names := map[string]database.InventoryItem{}
	for _, item := range items {
		if _, duplicate := names[item.Name]; duplicate {
			t.Fatalf("%s was proposed again: %+v", item.Name, items)
		}
		names[item.Name] = item
	}
	if len(names) != 3 || names["Pratos"].ID != decided.ID || names["Pratos"].Decision != "ACCEPTED" || names["Copos"].Decision != "REJECTED" || names["Talheres"].Decision != "PENDING" {
		t.Fatalf("items = %+v, want the decided items preserved and one fresh pending suggestion", items)
	}
	after := sourceKinds(t, f.h.seed, decided.ID)
	if len(before) != len(after) {
		t.Fatalf("a decided item's sources changed: before=%v after=%v", before, after)
	}
	for key, count := range before {
		if after[key] != count {
			t.Fatalf("a decided item's sources changed: before=%v after=%v", before, after)
		}
	}
	if after["HUMAN_REVIEW:quantity"] != 1 || after["VOICE:quantity"] != 1 {
		t.Fatalf("the decided item must keep its spoken source and its review: %v", after)
	}
}

func TestReviewedItemsCapTheRoomAtItsLimit(t *testing.T) {
	var items []database.InventoryItem
	for i := 0; i < 5; i++ {
		items = append(items, database.InventoryItem{ID: identity.NewID(), TypeCode: "OTHER", Name: string(rune('A' + i))})
	}
	evidence := []database.InventoryItemEvidence{{ID: identity.NewID(), ItemID: items[0].ID}, {ID: identity.NewID(), ItemID: items[3].ID}}
	reviewed := reviewedItems{keys: map[string]bool{itemKey("OTHER", " a "): true}, active: maxItemsPerEnvironment - 2}
	kept, keptEvidence, truncated := withoutReviewed(items, evidence, reviewed)
	if len(kept) != 2 || kept[0].Name != "B" || kept[1].Name != "C" || !truncated {
		t.Fatalf("kept = %+v truncated=%v, want the first two unreviewed suggestions that fit", kept, truncated)
	}
	if len(keptEvidence) != 0 {
		t.Fatalf("evidence of dropped suggestions must go too: %+v", keptEvidence)
	}
	if same, _, cut := withoutReviewed(items, evidence, reviewedItems{}); len(same) != 5 || cut {
		t.Fatal("without reviewed items nothing is dropped")
	}
	if itemKey("OTHER", "  Prato   Fundo ") != itemKey("OTHER", "prato fundo") || itemKey("SOFA", "x") == itemKey("OTHER", "x") {
		t.Fatal("item keys must ignore case and spacing but keep the type")
	}
}

func TestCatalogSourceRowsOfALargeRoomStayScopedToTheirItems(t *testing.T) {
	now := time.Now().UTC()
	version, tenant := identity.NewID(), identity.NewID()
	var items []database.InventoryItem
	var evidence []database.InventoryItemEvidence
	for i := 0; i < 80; i++ {
		item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, Name: "Item", Condition: "GOOD", Quantity: intp(1)}
		item.Suggested = []byte(`{"voice":[{"noteId":"` + identity.NewID().String() + `","revisionId":"` + identity.NewID().String() + `","attributes":["name","quantity"]}]}`)
		items = append(items, item)
		evidence = append(evidence, database.InventoryItemEvidence{ID: identity.NewID(), TenantID: tenant, ItemID: item.ID, Position: 0})
	}
	rows := catalogSourceRows(items, evidence, now)
	owner := map[identity.ID]bool{}
	for _, item := range items {
		owner[item.ID] = true
	}
	for _, row := range rows {
		if !owner[row.ItemID] || row.OriginVersionID != version || row.TenantID != tenant {
			t.Fatalf("source %+v escaped its item/version/tenant scope", row)
		}
	}
	if len(rows) != 80*4 {
		t.Fatalf("rows = %d, want 2 voice + 2 visual (name, condition) per item", len(rows))
	}
}

func TestCatalogNeverMergesOrSumsAndKeepsBothSignals(t *testing.T) {
	t.Run("IT-181 IT-184 IT-187 IT-194 the same group in two notes stays two suggestions with their own citations", func(t *testing.T) {
		f := newVoiceFlow(t)
		first, firstRevision := f.note(t, "cinco pratos no armário")
		second, secondRevision := f.note(t, "mais dois pratos na pia")
		f.seal(t)
		photo := f.env.photos[0].String()
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t,
				voiceItem{name: "Pratos do armário", quantity: intp(5), evidence: []string{photo}, citations: []map[string]any{cite(first, firstRevision, "name", "quantity")}},
				voiceItem{name: "Pratos da pia", quantity: intp(2), evidence: []string{photo}, citations: []map[string]any{cite(second, secondRevision, "name", "quantity")}},
			), nil
		})
		items := itemsOf(t, f.h.seed, f.env.env.ID)
		if len(items) != 2 {
			t.Fatalf("items = %+v, want two separate suggestions", items)
		}
		total := 0
		for _, item := range items {
			total += *item.Quantity
			if item.Decision != "PENDING" {
				t.Fatalf("%s is %s: a possible association is a human decision", item.Name, item.Decision)
			}
		}
		if total != 7 || *items[0].Quantity == 7 || *items[1].Quantity == 7 {
			t.Fatalf("quantities were summed or merged: %+v", items)
		}
		cited := map[identity.ID]bool{}
		for _, item := range items {
			for _, source := range ExtractVoiceSources(item) {
				cited[source.NoteID] = true
			}
		}
		if !cited[first] || !cited[second] {
			t.Fatalf("individual citations were lost: %v", cited)
		}
	})
	t.Run("IT-182 IT-183 a divergence stays visible: spoken value, image evidence and the stated limitation", func(t *testing.T) {
		f := newVoiceFlow(t)
		note, revision := f.note(t, "cinco, não, seis pratos")
		f.seal(t)
		photo := f.env.photos[0].String()
		limitation := "Fala diz seis pratos; a imagem mostra quatro."
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return strings.Replace(voiceCatalogJSON(t, voiceItem{name: "Pratos", quantity: intp(6), evidence: []string{photo}, citations: []map[string]any{cite(note, revision, "name", "quantity")}}), `"limitation":null`, `"limitation":"`+limitation+`"`, 1), nil
		})
		item := itemsOf(t, f.h.seed, f.env.env.ID)[0]
		if item.Quantity == nil || *item.Quantity != 6 || item.Limitation == nil || *item.Limitation != limitation || item.Decision != "PENDING" {
			t.Fatalf("item = %+v: the corrected spoken quantity, the stated divergence and the pending review must all remain", item)
		}
		kinds := sourceKinds(t, f.h.seed, item.ID)
		if kinds["VOICE:quantity"] != 1 || kinds["VISUAL:name"] != 1 || len(evidenceOf(t, f.h.seed, item.ID)) != 1 {
			t.Fatalf("sources = %v: both signals must be recorded", kinds)
		}
		var revisionRow database.TranscriptRevision
		if err := f.h.seed.DB.Where("id=?", revision).Take(&revisionRow).Error; err != nil || revisionRow.Text != "cinco, não, seis pratos" {
			t.Fatalf("the transcript wording must be retained: %+v err=%v", revisionRow, err)
		}
	})
	t.Run("IT-185 IT-186 a negative quantity is rejected and a missing count stays unspecified", func(t *testing.T) {
		f := newVoiceFlow(t)
		note, revision := f.note(t, "menos dois copos e alguns pratos")
		f.seal(t)
		photo := f.env.photos[0].String()
		f.run(t, func(llm.StructuredRequest) (string, error) {
			return voiceCatalogJSON(t,
				voiceItem{name: "Copos", quantity: intp(-2), evidence: []string{photo}, citations: []map[string]any{cite(note, revision, "name", "quantity")}},
				voiceItem{name: "Taças", quantity: intp(0), evidence: []string{photo}},
				voiceItem{name: "Pratos", citations: []map[string]any{cite(note, revision, "name")}},
			), nil
		})
		items := itemsOf(t, f.h.seed, f.env.env.ID)
		if len(items) != 1 || items[0].Name != "Pratos" || items[0].Quantity != nil {
			t.Fatalf("items = %+v, want only the item without a count, with the quantity unspecified", items)
		}
	})
}
