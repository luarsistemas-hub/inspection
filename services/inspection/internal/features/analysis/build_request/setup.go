// Package build_request assembles authorized model requests for a comparison job.
package build_request

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/comparative"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type Dependencies struct {
	Store objectstore.Store
}

// Setup returns the request builder used by the comparison processor.
func Setup(deps Dependencies) (processcomparison.RequestBuilder, error) {
	if deps.Store.Client == nil || deps.Store.Bucket == "" {
		return nil, fmt.Errorf("slice analysis/build_request: missing object store")
	}
	return build(deps.Store), nil
}

func build(store objectstore.Store) processcomparison.RequestBuilder {
	return func(ctx context.Context, tx *gorm.DB, job database.ComparisonJob, _ processcomparison.Payload) (llm.StructuredRequest, error) {
		var answers []database.RequirementAnswer
		if err := tx.WithContext(ctx).Where("tenant_id=? AND requirement_key=? AND draft_id IN (SELECT d.id FROM capture.capture_drafts d JOIN inspections.responsibilities r ON r.id=d.responsibility_id WHERE d.tenant_id=? AND r.inspection_id=?)", job.TenantID, job.RequirementKey, job.TenantID, job.InspectionID).Order("updated_at DESC").Limit(1).Find(&answers).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		if len(answers) == 0 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: requirement evidence not found")
		}
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, job.InspectionID).First(&inspection).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		var promptSnapshot database.AnalysisPromptSnapshot
		if err := tx.WithContext(ctx).Where("id=?", inspection.AnalysisPromptSnapshotID).First(&promptSnapshot).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		profileDocument, err := analysisprompt.Parse(promptSnapshot.DefinitionJSON)
		if err != nil {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: invalid pinned prompt")
		}
		modelAlias, systemPrompt, outputSchema, minimumConfidence := profileDocument.ModelAlias, profileDocument.SystemPrompt, profileDocument.OutputSchema, profileDocument.MinimumConfidenceBPS
		var draft database.CaptureDraft
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, answers[0].DraftID).First(&draft).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		var requirements []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: invalid requirement context")
		}
		var requirement capturecore.Requirement
		for _, candidate := range requirements {
			if candidate.Key == job.RequirementKey {
				requirement = candidate
				break
			}
		}
		if requirement.Key == "" {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: requirement context not found")
		}
		comparisonMode := "CURRENT_ONLY"
		var ids []identity.ID
		if err := json.Unmarshal(answers[0].MediaIDs, &ids); err != nil || len(ids) == 0 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: no evidence")
		}
		current := make([]comparative.Evidence, 0, len(ids))
		for _, mediaID := range ids {
			var derivative database.MediaDerivative
			if err := tx.Where("tenant_id=? AND media_id=? AND kind=?", job.TenantID, mediaID, "ANALYSIS").Order("created_at DESC").First(&derivative).Error; err != nil {
				return llm.StructuredRequest{}, err
			}
			data, err := store.Read(ctx, derivative.ObjectKey)
			if err != nil {
				return llm.StructuredRequest{}, err
			}
			digest := sha256.Sum256(data)
			current = append(current, comparative.Evidence{ID: mediaID, Source: "CURRENT", Digest: hex.EncodeToString(digest[:]), DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)})
		}
		schema, err := json.Marshal(outputSchema)
		if err != nil {
			return llm.StructuredRequest{}, err
		}
		userPrompt := fmt.Sprintf("Requisito: %s\nTítulo: %s\nInstruções da captura: %s\nModo: %s\nConfiança mínima: %.2f\n\nAs imagens seguintes estão identificadas individualmente.", requirement.Key, requirement.Label, requirement.Instructions, comparisonMode, float64(minimumConfidence)/10000)
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", job.TenantID, job.InspectionID).First(&reference).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		if reference.ReferenceVersionID == nil || reference.ComparisonMode != "FIXED_ORIGIN" {
			return llm.StructuredRequest{ModelAlias: modelAlias, PromptDigest: promptSnapshot.CanonicalDigest, Mode: comparisonMode, SystemPrompt: systemPrompt, UserPrompt: userPrompt, JSONSchema: schema, MinimumConfidenceBPS: minimumConfidence, Images: mustCurrentImages(current)}, nil
		}
		var snapshot struct {
			Items []struct {
				MediaID identity.ID `json:"mediaId"`
			} `json:"items"`
		}
		if err := json.Unmarshal(reference.Payload, &snapshot); err != nil || len(snapshot.Items) == 0 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: pinned origin evidence not found")
		}
		const originRequirementPrefix = "origin:"
		if !strings.HasPrefix(job.RequirementKey, originRequirementPrefix) {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: requirement has no pinned origin pair")
		}
		originMediaID, err := identity.ParseID(strings.TrimPrefix(job.RequirementKey, originRequirementPrefix))
		if err != nil {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: invalid pinned origin pair")
		}
		pairID := "pair:" + originMediaID.String()
		for index := range current {
			current[index].PairID = pairID
			current[index].Position = fmt.Sprintf("CURRENT_%d", index+1)
		}
		origin := make([]comparative.Evidence, 0, 1)
		for _, item := range snapshot.Items {
			if item.MediaID != originMediaID {
				continue
			}
			var derivative database.MediaDerivative
			if err := tx.Where("tenant_id=? AND media_id=? AND kind=?", job.TenantID, item.MediaID, "ANALYSIS").Order("created_at DESC").First(&derivative).Error; err != nil {
				return llm.StructuredRequest{}, err
			}
			data, err := store.Read(ctx, derivative.ObjectKey)
			if err != nil {
				return llm.StructuredRequest{}, err
			}
			digest := sha256.Sum256(data)
			origin = append(origin, comparative.Evidence{ID: item.MediaID, Source: "ORIGIN", PairID: pairID, Position: "ORIGIN", Digest: hex.EncodeToString(digest[:]), DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)})
			break
		}
		if len(origin) != 1 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: pinned origin evidence not found for requirement")
		}
		comparisonMode = "COMPARE_ORIGIN_CURRENT"
		userPrompt = fmt.Sprintf("Requisito: %s\nTítulo: %s\nInstruções da captura: %s\nModo: %s\nConfiança mínima: %.2f\n\nAs imagens seguintes estão identificadas individualmente.", requirement.Key, requirement.Label, requirement.Instructions, comparisonMode, float64(minimumConfidence)/10000)
		images, _, err := comparative.Build(current, origin)
		if err != nil {
			return llm.StructuredRequest{}, err
		}
		return llm.StructuredRequest{ModelAlias: modelAlias, PromptDigest: promptSnapshot.CanonicalDigest, Mode: comparisonMode, SystemPrompt: systemPrompt, UserPrompt: userPrompt, JSONSchema: schema, MinimumConfidenceBPS: minimumConfidence, Images: images}, nil
	}
}

func mustCurrentImages(evidence []comparative.Evidence) []llm.NormalizedImage {
	images := make([]llm.NormalizedImage, 0, len(evidence))
	for _, item := range evidence {
		images = append(images, llm.NormalizedImage{EvidenceID: item.ID.String(), Source: item.Source, Digest: item.Digest, DataURL: item.DataURL})
	}
	return images
}
