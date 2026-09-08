package resolvers

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	publication "inspection/services/inspection/internal/features/reports/publication"
	schedulecore "inspection/services/inspection/internal/features/schedules/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type Resolver struct {
	Bus                *mediator.Bus
	DB                 *gorm.DB
	Invitations        invitationcore.Service
	ScheduleService    schedulecore.Service
	InspectionService  inspectioncore.Service
	ProjectService     projectcore.Service
	PublicationService publication.Service
	Store              objectstore.Store
	Authorizer         auth.Authorizer
}
