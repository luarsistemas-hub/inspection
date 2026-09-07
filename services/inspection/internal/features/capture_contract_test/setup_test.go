package capture_contract_test

import (
	"testing"

	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturedeclare "inspection/services/inspection/internal/features/capture/declare_impossibility"
	capturefinalize "inspection/services/inspection/internal/features/capture/finalize_submission"
	capturesave "inspection/services/inspection/internal/features/capture/save_metadata"
	capturesubmit "inspection/services/inspection/internal/features/capture/submit"
	acceptprocessing "inspection/services/inspection/internal/features/invitations/accept_processing"
	dispatchcapture "inspection/services/inspection/internal/features/invitations/dispatch_capture_invitation"
	mediacomplete "inspection/services/inspection/internal/features/media/complete_upload"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	mediafalsepositive "inspection/services/inspection/internal/features/media/declare_false_positive"
	mediapresign "inspection/services/inspection/internal/features/media/presign_parts"
	processmedia "inspection/services/inspection/internal/features/media/process_verified"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origininvalidate "inspection/services/inspection/internal/features/origins/invalidate_version"
	origininvite "inspection/services/inspection/internal/features/origins/invite_capture"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	recaptureexpire "inspection/services/inspection/internal/features/recapture/expire_request"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	recapturesubmit "inspection/services/inspection/internal/features/recapture/submit"
)

func TestTask5OperationSetupsRequireDependencies(t *testing.T) {
	setups := []func() error{
		func() error { return acceptprocessing.Setup(acceptprocessing.Dependencies{}) },
		func() error { return capturebootstrap.Setup(capturebootstrap.Dependencies{}) },
		func() error { return capturedeclare.Setup(capturedeclare.Dependencies{}) },
		func() error { _, err := capturefinalize.Setup(capturefinalize.Dependencies{}); return err },
		func() error { return capturesave.Setup(capturesave.Dependencies{}) },
		func() error { return capturesubmit.Setup(capturesubmit.Dependencies{}) },
		func() error { _, err := dispatchcapture.Setup(dispatchcapture.Dependencies{}); return err },
		func() error { return mediacomplete.Setup(mediacomplete.Dependencies{}) },
		func() error { return mediacreate.Setup(mediacreate.Dependencies{}) },
		func() error { return mediafalsepositive.Setup(mediafalsepositive.Dependencies{}) },
		func() error { return mediapresign.Setup(mediapresign.Dependencies{}) },
		func() error { _, err := processmedia.Setup(processmedia.Dependencies{}); return err },
		func() error { return originactivate.Setup(originactivate.Dependencies{}) },
		func() error { return origininvalidate.Setup(origininvalidate.Dependencies{}) },
		func() error { return origininvite.Setup(origininvite.Dependencies{}) },
		func() error { return originlist.Setup(originlist.Dependencies{}) },
		func() error { return recaptureexpire.Setup(recaptureexpire.Dependencies{}) },
		func() error { return recapturerequest.Setup(recapturerequest.Dependencies{}) },
		func() error { return recapturesubmit.Setup(recapturesubmit.Dependencies{}) },
	}
	for index, setup := range setups {
		if err := setup(); err == nil {
			t.Fatalf("setup %d accepted missing dependencies", index)
		}
	}
}
