package resolvers

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	adminactivation "inspection/services/inspection/internal/features/onboarding/admin_activation"
	onboardingbootstrap "inspection/services/inspection/internal/features/onboarding/onboarding_bootstrap"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	publication "inspection/services/inspection/internal/features/reports/publication"
	schedulecore "inspection/services/inspection/internal/features/schedules/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/keycloak"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type Resolver struct {
	Bus                 *mediator.Bus
	DB                  *gorm.DB
	Invitations         invitationcore.Service
	ScheduleService     schedulecore.Service
	InspectionService   inspectioncore.Service
	ProjectService      projectcore.Service
	PublicationService  publication.Service
	Store               objectstore.Store
	Authorizer          auth.Authorizer
	Onboarding          onboardingsession.Service
	AdminActivation     adminactivation.Service
	OnboardingBootstrap onboardingbootstrap.Service
	OwnerProvider       keycloak.OwnerIdentityProvider
	OwnerIssuer         string
}
