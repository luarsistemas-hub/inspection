// Package testfailure provides a database-boundary failure hook for API tests.
// Reuse the operation allowlist and boundary fixtures for API07/API14 by
// naming each operation and injecting errors at its database adapter boundary.
package testfailure

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/gorm"
)

const (
	HeaderOperation = "X-Inspection-Test-Operation"
	HeaderFailure   = "X-Inspection-Test-Failure"
	Secret          = "qa-secret-123"
)

type failure struct{ operation, kind string }
type contextKey struct{}

// WithRequest enables a requested failure only for test environments and the
// explicitly named GraphQL operation.
func WithRequest(r *http.Request, environment string) *http.Request {
	if environment != "test" {
		return r
	}
	operation := strings.TrimSpace(r.Header.Get(HeaderOperation))
	if operation != "externalCapture" && operation != "acceptProcessing" && operation != "beginVideoCapture" &&
		operation != "createInspection" && operation != "createSchedule" && operation != "planInspection" &&
		operation != "configureRetentionPolicy" && operation != "recordDeletionRequest" && operation != "applyLegalHold" && operation != "releaseLegalHold" &&
		operation != "inviteOriginCapture" && operation != "activateOriginVersion" && operation != "invalidateOriginVersion" && operation != "originVersions" && operation != "promoteInspectionVideo" &&
		operation != "saveCaptureMetadata" && operation != "submitCapture" && operation != "submitRecapture" &&
		operation != "videoPreparation" && operation != "retryVideoPreparation" && operation != "reprocessVideoPreparation" && operation != "reanalyzeVideoInspection" &&
		operation != "report" && operation != "reports" && operation != "customerReport" && operation != "customerEvidence" &&
		operation != "configurePublicationPolicy" && operation != "publishReport" && operation != "invalidateReportPublication" && operation != "publicationPolicy" && operation != "reportDownload" && operation != "requestRecapture" {
		return r
	}
	kind := strings.TrimSpace(r.Header.Get(HeaderFailure))
	if kind != "dependency" && kind != "internal" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), contextKey{}, failure{operation: operation, kind: kind}))
}

// Install registers callbacks that fail at the GORM I/O boundary when the
// request context carries an enabled test failure. It is safe to install only
// once per database handle.
func Install(db *gorm.DB) error {
	if db == nil {
		return errors.New("test failure hook requires a database")
	}
	callback := func(tx *gorm.DB) {
		injected, ok := tx.Statement.Context.Value(contextKey{}).(failure)
		if !ok {
			return
		}
		if authenticationTable(tx.Statement.Table) {
			return
		}
		// Tenant session settings and the runtime-role guard are transaction
		// plumbing, not the adapter whose failure the request simulates.
		if sql := tx.Statement.SQL.String(); strings.Contains(sql, "set_config") || strings.Contains(sql, "pg_roles") {
			return
		}
		if boundary := operationBoundary(injected.operation); boundary != "" && !matchesBoundary(tx.Statement.Table, boundary) {
			return
		}
		switch injected.kind {
		case "dependency":
			tx.AddError(apperror.New(apperror.DependencyUnavailable, "database", "database unavailable"))
		case "internal":
			tx.AddError(errors.New("adapter failure: " + Secret))
		}
	}
	registrations := []func() error{
		func() error {
			return db.Callback().Query().Before("gorm:query").Register("inspection:test_failure:query", callback)
		},
		func() error {
			return db.Callback().Create().Before("gorm:create").Register("inspection:test_failure:create", callback)
		},
		func() error {
			return db.Callback().Update().Before("gorm:update").Register("inspection:test_failure:update", callback)
		},
		func() error {
			return db.Callback().Delete().Before("gorm:delete").Register("inspection:test_failure:delete", callback)
		},
		func() error {
			return db.Callback().Row().Before("gorm:row").Register("inspection:test_failure:row", callback)
		},
	}
	for _, register := range registrations {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}

func matchesBoundary(table, boundary string) bool {
	if table == boundary {
		return true
	}
	separator := strings.LastIndex(boundary, ".")
	if separator < 0 {
		return false
	}
	name := boundary[separator+1:]
	return table == name || strings.HasSuffix(table, "."+name)
}

// authenticationTable lets request authentication, external session and CSRF
// checks complete before the injected failure reaches slice-owned database I/O.
func authenticationTable(table string) bool {
	name := strings.ToLower(table)
	for _, protected := range []string{
		"external_sessions", "sessions", "user_sessions", "onboarding_sessions",
		"admin_activation_sessions", "user_invitation_sessions", "identities",
		"memberships", "invitations",
	} {
		if strings.HasSuffix(name, "."+protected) || name == protected {
			return true
		}
	}
	return false
}

func operationBoundary(operation string) string {
	switch operation {
	case "externalCapture":
		return "media.video_policies"
	case "acceptProcessing":
		return "invitations.processing_acceptances"
	case "beginVideoCapture":
		return "capture.video_attempts"
	case "saveCaptureMetadata":
		return "media.media_objects"
	case "submitCapture":
		return "capture.submission_versions"
	case "submitRecapture":
		return "recapture.requests"
	default:
		return ""
	}
}
