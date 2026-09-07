package lifecycle_contract_test

import (
	"testing"

	inspectioncancel "inspection/services/inspection/internal/features/inspections/cancel"
	inspectionmanual "inspection/services/inspection/internal/features/inspections/create_manual"
	inspectionoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	inspectionget "inspection/services/inspection/internal/features/inspections/get"
	inspectioninvalidate "inspection/services/inspection/internal/features/inspections/invalidate"
	inspectionlist "inspection/services/inspection/internal/features/inspections/list"
	projectadd "inspection/services/inspection/internal/features/projects/add_exceptional_stage"
	projectclose "inspection/services/inspection/internal/features/projects/close_project"
	projectcreate "inspection/services/inspection/internal/features/projects/create_project"
	projectget "inspection/services/inspection/internal/features/projects/get_project"
	projectlist "inspection/services/inspection/internal/features/projects/list_projects"
	projectreopen "inspection/services/inspection/internal/features/projects/reopen_project"
	projectskip "inspection/services/inspection/internal/features/projects/skip_stage"
	projectstart "inspection/services/inspection/internal/features/projects/start_stage"
	schedulecancel "inspection/services/inspection/internal/features/schedules/cancel_schedule"
	schedulecreate "inspection/services/inspection/internal/features/schedules/create_schedule"
	schedulelist "inspection/services/inspection/internal/features/schedules/list_schedules"
	materializedue "inspection/services/inspection/internal/features/schedules/materialize_due"
	schedulereminders "inspection/services/inspection/internal/features/schedules/schedule_reminders"
	scheduleupdate "inspection/services/inspection/internal/features/schedules/update_schedule"
)

func TestLifecycleSetupsRequireDependencies(t *testing.T) {
	setups := []func() error{
		func() error { return inspectioncancel.Setup(inspectioncancel.Dependencies{}) },
		func() error { return inspectionmanual.Setup(inspectionmanual.Dependencies{}) },
		func() error { return inspectionoccurrence.Setup(inspectionoccurrence.Dependencies{}) },
		func() error { return inspectionget.Setup(inspectionget.Dependencies{}) },
		func() error { return inspectioninvalidate.Setup(inspectioninvalidate.Dependencies{}) },
		func() error { return inspectionlist.Setup(inspectionlist.Dependencies{}) },
		func() error { return projectadd.Setup(projectadd.Dependencies{}) },
		func() error { return projectclose.Setup(projectclose.Dependencies{}) },
		func() error { return projectcreate.Setup(projectcreate.Dependencies{}) },
		func() error { return projectget.Setup(projectget.Dependencies{}) },
		func() error { return projectlist.Setup(projectlist.Dependencies{}) },
		func() error { return projectreopen.Setup(projectreopen.Dependencies{}) },
		func() error { return projectskip.Setup(projectskip.Dependencies{}) },
		func() error { return projectstart.Setup(projectstart.Dependencies{}) },
		func() error { return schedulecancel.Setup(schedulecancel.Dependencies{}) },
		func() error { return schedulecreate.Setup(schedulecreate.Dependencies{}) },
		func() error { return schedulelist.Setup(schedulelist.Dependencies{}) },
		func() error { return materializedue.Setup(materializedue.Dependencies{}) },
		func() error { return schedulereminders.Setup(schedulereminders.Dependencies{}) },
		func() error { return scheduleupdate.Setup(scheduleupdate.Dependencies{}) },
	}
	for index, setup := range setups {
		if err := setup(); err == nil {
			t.Fatalf("setup %d accepted missing dependencies", index)
		}
	}
}
