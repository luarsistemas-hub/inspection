package resolvers

import (
	"strings"
	"time"

	mediavideoprep "inspection/services/inspection/internal/features/media/manage_video_preparation"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
)

func mapVideoPreparationStatus(status mediavideoprep.Status) *graphql1.VideoPreparationStatus {
	result := &graphql1.VideoPreparationStatus{MediaID: status.MediaID.String(), PreparationID: status.PreparationID.String(), Status: status.Status, Stage: status.Stage, PreparationVersion: status.PreparationVersion, Attempts: status.Attempts, ProcessedFrames: status.ProcessedFrames, FramesSampled: status.FramesSampled, FramesRetained: status.FramesRetained, FramesRepresentative: status.FramesRepresentative, ErrorCode: status.ErrorCode, Retryable: status.Retryable, SourceAvailable: status.SourceAvailable, ManifestDigest: status.ManifestDigest}
	if status.FirstFrameReadyAt != nil {
		value := status.FirstFrameReadyAt.UTC().Format(time.RFC3339Nano)
		result.FirstFrameReadyAt = &value
	}
	if status.SourceExpiryAt != nil {
		value := status.SourceExpiryAt.UTC().Format(time.RFC3339Nano)
		result.SourceExpiryAt = &value
	}
	return result
}

func safeVideoPreparationError(code string) string {
	switch strings.TrimSpace(code) {
	case "CHECKSUM_MISMATCH", "DURATION_EXCEEDED", "FRAME_INTEGRITY_FAILED", "MODEL_OUTPUT_INVALID", "NO_FRAMES", "NO_USABLE_SCOPE", "PREPARATION_FAILED", "PROVIDER_CONFIGURATION", "PROVIDER_UNAVAILABLE", "RESOURCE_LIMIT", "SENSITIVE_CONTENT", "SOURCE_INVALID", "STORE_UNAVAILABLE", "TIMEOUT":
		return strings.TrimSpace(code)
	default:
		return "PREPARATION_FAILED"
	}
}
