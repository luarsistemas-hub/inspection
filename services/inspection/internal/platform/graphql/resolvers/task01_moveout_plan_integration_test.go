//go:build integration

package resolvers

import (
	"strings"
	"testing"

	"gorm.io/gorm"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
)

func TestTask01IT057PlanInspectionProjectAndStageRequireBaseline(t *testing.T) {
	fx := newInspectionModeGraphQLFixtureWithSchemaAndVideo(t, false, catalog.FixedOrigin, catalog.DescriptiveSchemaVersion, false)
	for _, path := range []string{"project_and_stage", "existing_project_and_stage"} {
		t.Run(path, func(t *testing.T) {
			clientID := "IT-057-" + path
			projectFields := `createProject:true,projectName:"IT-057 project",stages:[{key:"initial",label:"Inspeção inicial"}]`
			var existingProjectName string
			if path == "existing_project_and_stage" {
				created, err := (projectcore.Service{DB: fx.h.DB, Bus: fx.bus}).Create(fx.ctx, projectcore.CreateInput{
					TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, Name: clientID,
					Stages:         []projectcore.PlannedStageInput{{Key: "initial", Label: "Inspeção inicial"}},
					IdempotencyKey: clientID + "/seed-project",
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(created.Stages) != 1 {
					t.Fatalf("expected one seeded project stage, got %d", len(created.Stages))
				}
				existingProjectName = created.Project.Name
				projectFields = `projectId:"` + created.Project.ID.String() + `",stageId:"` + created.Stages[0].ID.String() + `"`
			}
			query := `mutation { planInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",templateId:"` + fx.templateID.String() + `",comparisonMode:FIXED_ORIGIN,` + projectFields + `,dueAt:"2026-10-17T12:00:00Z",deadlineAt:"2026-10-18T12:00:00Z",reason:"` + clientID + `",clientMutationId:"` + clientID + `"}) { inspection { id } project { id } userErrors { code field message } clientMutationId } }`
			response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
			if !containsAll(response, `"code":"INVALID_STATE"`, `"field":"referenceVersionId"`, "catalogada e aprovada") {
				t.Fatalf("IT-057 planInspection %s did not expose the baseline guard: %s", path, response)
			}
			if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
				var inspections int64
				if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source_key=?", fx.seed.TenantID, clientID+"/inspection").Count(&inspections).Error; err != nil {
					return err
				}
				if inspections != 0 {
					t.Fatalf("IT-057 persisted %d inspections after rejecting planInspection", inspections)
				}
				if path == "project_and_stage" {
					var projects int64
					if err := tx.Model(&database.Project{}).Where("tenant_id=? AND name=?", fx.seed.TenantID, "IT-057 project").Count(&projects).Error; err != nil {
						return err
					}
					if projects != 0 {
						t.Fatalf("IT-057 left %d projects from the rolled back plan", projects)
					}
				} else if existingProjectName == "" {
					t.Fatal("IT-057 existing project fixture was not created")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
