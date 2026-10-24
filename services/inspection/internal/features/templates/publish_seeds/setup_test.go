package publish_seeds

import (
	"context"
	"encoding/json"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/core"
	publishtemplate "inspection/services/inspection/internal/features/templates/publish_template"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestCuratedSeedCommandDispatchesV2PayloadsAndStableKeys(t *testing.T) {
	tenant := identity.NewID()
	bus := mediator.New()
	type publishedVersion struct {
		key            string
		id             identity.ID
		number, schema int
	}
	versionsByKey := map[string]int{}
	byIdempotency := map[string]publishedVersion{}
	for _, key := range []string{"property-periodic", "construction-progress", "cleaning-quality"} {
		versionsByKey[key] = 1
	}
	if err := bus.RegisterCommand(publishtemplate.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(publishtemplate.Command)
		if existing, ok := byIdempotency[command.IdempotencyKey]; ok {
			return core.View{Version: database.TemplateVersion{ID: existing.id, VersionNumber: existing.number, SchemaVersion: existing.schema}}, nil
		}
		var doc struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		if err := json.Unmarshal(command.DefinitionJSON, &doc); err != nil {
			return nil, err
		}
		versionsByKey[command.Key]++
		published := publishedVersion{key: command.Key, id: identity.NewID(), number: versionsByKey[command.Key], schema: doc.SchemaVersion}
		byIdempotency[command.IdempotencyKey] = published
		return core.View{Version: database.TemplateVersion{ID: published.id, VersionNumber: published.number, SchemaVersion: published.schema}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Setup(Dependencies{Bus: bus}); err != nil {
		t.Fatal(err)
	}
	command := Command{TenantID: tenant, AnalysisType: "REAL_ESTATE", PropertySegmentVersionID: identity.NewID(), ConstructionSegmentVersionID: identity.NewID(), CleaningSegmentVersionID: identity.NewID()}
	firstRaw, err := bus.Send(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	first := firstRaw.(Result)
	if len(first.Versions) != 3 || len(byIdempotency) != 3 {
		t.Fatalf("published versions: %+v", first)
	}
	for key := range first.Versions {
		var found *publishedVersion
		for _, version := range byIdempotency {
			if version.key == key {
				v := version
				found = &v
			}
		}
		if found == nil || found.number != 2 || found.schema != 2 {
			t.Fatalf("%s did not publish v2 after v1: %+v", key, found)
		}
	}
	secondRaw, err := bus.Send(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second := secondRaw.(Result)
	if len(byIdempotency) != 3 {
		t.Fatalf("replay created additional versions: %d", len(byIdempotency))
	}
	for key, id := range first.Versions {
		if second.Versions[key] != id {
			t.Fatalf("replay changed %s version id", key)
		}
	}
}
