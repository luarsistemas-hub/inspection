package resolvers

import (
	"context"
	"log/slog"
	"strings"

	"inspection/libs/identity"
	revisepairing "inspection/services/inspection/internal/features/analysis/revise_comparison_pairing"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
)

// comparisonOutcomeFilter validates the optional comparison filter of a
// customer report before any report data is read.
func comparisonOutcomeFilter(value *graphql1.ComparisonOutcome) (string, error) {
	if value == nil {
		return "", nil
	}
	if !value.IsValid() || !reportcore.ValidComparisonOutcome(string(*value)) {
		return "", invalidComparisonFilter()
	}
	return string(*value), nil
}

func invalidComparisonFilter() error {
	return apperror.New(apperror.InvalidInput, "comparisonOutcome", "Filtro de comparação inválido.")
}

// reviseComparisonPairing records a staff pairing decision for one baseline
// item and queues the comparison generation that issues the next version.
func (r *mutationResolver) reviseComparisonPairing(ctx context.Context, input graphql1.ReviseComparisonPairingInput) (*graphql1.ReviseComparisonPairingPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	// Only staff who may correct reports revise a comparison; internal
	// viewers and customers are refused before any data is read.
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, true, auth.Manager, auth.Employee); err != nil {
		return nil, err
	}
	payload := &graphql1.ReviseComparisonPairingPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	inspectionID, err := identity.ParseID(input.InspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	if err := authorizeReportResource(ctx, r.DB, meta, inspectionID, "inspectionId", "Laudo não encontrado."); err != nil {
		return nil, err
	}
	itemID, err := identity.ParseID(input.ItemIdentityID)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "itemIdentityId", "Informe um item válido da referência.")))
		return payload, nil
	}
	command := revisepairing.Command{TenantID: meta.TenantID, ActorID: meta.Principal.MembershipID, InspectionID: inspectionID, ItemIdentityID: itemID, ExpectedVersion: input.ExpectedVersion, ClientMutationID: strings.TrimSpace(input.ClientMutationID)}
	for _, pair := range input.Pairs {
		if pair == nil {
			continue
		}
		baseline, baselineErr := identity.ParseID(pair.BaselineEvidenceID)
		current, currentErr := identity.ParseID(pair.CurrentEvidenceID)
		if baselineErr != nil || currentErr != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "pairs", "Uma das evidências informadas é inválida.")))
			return payload, nil
		}
		command.Pairs = append(command.Pairs, revisepairing.Pair{BaselineEvidenceID: baseline, CurrentEvidenceID: current})
	}
	operation, err := revisepairing.Setup(revisepairing.Dependencies{DB: r.DB, Within: r.WithinTenant})
	if err != nil {
		return nil, err
	}
	result, err := operation(ctx, command)
	if err != nil {
		if code, _, _ := apperror.Public(err); code == apperror.Internal {
			slog.ErrorContext(ctx, "comparison_pairing_revision_failed", "tenantId", meta.TenantID.String(), "inspectionId", inspectionID.String(), "error", err)
		}
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	payload.Report = mapReport(result.Report)
	return payload, nil
}

func mapInternalComparison(section *reportcore.ComparisonSection) *graphql1.ReportComparison {
	if section == nil {
		return nil
	}
	out := &graphql1.ReportComparison{Baseline: &graphql1.ReportComparisonBaseline{VersionID: section.Baseline.VersionID, VersionNumber: section.Baseline.VersionNumber, Purpose: section.Baseline.Purpose, PurposeLabel: reportcore.CodeLabel(section.Baseline.Purpose), ActivatedAt: section.Baseline.ActivatedAt}, Status: section.Status, StatusLabel: reportcore.ComparisonStatusLabel(section.Status), Counts: mapComparisonCounts(section.Counts), Items: make([]*graphql1.ReportComparisonItem, 0, len(section.Items)), OutsideBaseline: make([]*graphql1.ReportComparisonObservation, 0, len(section.OutsideBaseline))}
	for _, item := range section.Items {
		mapped := &graphql1.ReportComparisonItem{ItemIdentityID: optional(item.ItemIdentityID), Room: item.Room, TypeCode: optional(item.TypeCode), Name: item.Name, Outcome: graphql1.ComparisonOutcome(item.Outcome), OutcomeLabel: reportcore.ComparisonOutcomeLabel(item.Outcome), BaselineCondition: optional(item.BaselineCondition), CurrentCondition: optional(item.CurrentCondition), Description: optional(item.Description), Limitations: append([]string{}, item.Limitations...), Pairs: make([]*graphql1.ReportComparisonPair, 0, len(item.Pairs)), PairingRevised: item.PairingRevised}
		if item.Outcome == reportcore.ComparisonNoBasis {
			// "No basis to compare" never states a baseline condition.
			mapped.BaselineCondition = nil
		}
		for _, pair := range item.Pairs {
			mapped.Pairs = append(mapped.Pairs, &graphql1.ReportComparisonPair{BaselineEvidenceID: pair.BaselineEvidenceID, CurrentEvidenceID: pair.CurrentEvidenceID})
		}
		out.Items = append(out.Items, mapped)
	}
	for _, observation := range section.OutsideBaseline {
		out.OutsideBaseline = append(out.OutsideBaseline, &graphql1.ReportComparisonObservation{Room: observation.Room, TypeCode: optional(observation.TypeCode), Name: observation.Name, Description: optional(observation.Description)})
	}
	return out
}

func mapCustomerComparison(view *customerreport.ComparisonView) *graphql1.CustomerComparison {
	if view == nil {
		return nil
	}
	out := &graphql1.CustomerComparison{BaselineVersionNumber: view.BaselineVersionNumber, Purpose: view.Purpose, PurposeLabel: view.PurposeLabel, ActivatedAt: view.ActivatedAt, Status: view.Status, StatusLabel: view.StatusLabel, Counts: mapComparisonCounts(view.Counts), Displayed: view.Displayed, Items: make([]*graphql1.CustomerComparisonItem, 0, len(view.Items)), OutsideBaseline: make([]*graphql1.CustomerComparisonObservation, 0, len(view.OutsideBaseline)), Disclaimer: view.Disclaimer}
	for _, item := range view.Items {
		mapped := &graphql1.CustomerComparisonItem{Room: item.Room, Name: item.Name, Outcome: graphql1.ComparisonOutcome(item.Outcome), OutcomeLabel: item.OutcomeLabel, BaselineCondition: optional(item.BaselineCondition), CurrentCondition: optional(item.CurrentCondition), Limitations: append([]string{}, item.Limitations...), Pairs: make([]*graphql1.CustomerComparisonPair, 0, len(item.Pairs))}
		for _, pair := range item.Pairs {
			mapped.Pairs = append(mapped.Pairs, &graphql1.CustomerComparisonPair{BaselineEvidence: pair.BaselineEvidence, CurrentEvidence: pair.CurrentEvidence, BaselineAvailability: pair.BaselineAvailability, CurrentAvailability: pair.CurrentAvailability})
		}
		out.Items = append(out.Items, mapped)
	}
	for _, observation := range view.OutsideBaseline {
		out.OutsideBaseline = append(out.OutsideBaseline, &graphql1.CustomerComparisonObservation{Room: observation.Room, Name: observation.Name})
	}
	return out
}

func mapComparisonCounts(counts reportcore.ComparisonCounts) *graphql1.ComparisonCounts {
	return &graphql1.ComparisonCounts{Total: counts.Total, Unchanged: counts.Unchanged, Changed: counts.Changed, Improved: counts.Improved, Missing: counts.Missing, NotObserved: counts.NotObserved, NoBasis: counts.NoBasis}
}
