package publish_seeds

import (
	"context"
	"encoding/json"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/features/templates/core"
	publishtemplate "inspection/services/inspection/internal/features/templates/publish_template"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID                     identity.ID
	AnalysisProfileID            identity.ID
	PropertySegmentVersionID     identity.ID
	ConstructionSegmentVersionID identity.ID
	CleaningSegmentVersionID     identity.ID
}
type Result struct{ Versions map[string]identity.ID }
type Dependencies struct{ Bus *mediator.Bus }

func Setup(d Dependencies) error {
	if d.Bus == nil {
		return fmt.Errorf("slice templates/publish_seeds: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		cmd := raw.(Command)
		seeds := catalog.CuratedSeeds(cmd.AnalysisProfileID.String(), cmd.PropertySegmentVersionID.String(), cmd.ConstructionSegmentVersionID.String(), cmd.CleaningSegmentVersionID.String())
		result := Result{Versions: map[string]identity.ID{}}
		for _, seed := range seeds {
			payload, err := json.Marshal(seed.Document)
			if err != nil {
				return nil, err
			}
			published, err := d.Bus.Send(ctx, publishtemplate.Command{TenantID: cmd.TenantID, Key: seed.TemplateKey, Name: seed.TemplateKey, IdempotencyKey: "curated-seed:" + seed.TemplateKey + ":" + seed.Document.SegmentVersionID, DefinitionJSON: payload})
			if err != nil {
				return nil, err
			}
			result.Versions[seed.TemplateKey] = published.(core.View).Version.ID
		}
		return result, nil
	})
}
