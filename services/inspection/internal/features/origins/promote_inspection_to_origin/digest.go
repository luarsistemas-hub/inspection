package promote_inspection_to_origin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// DigestInput contains the values whose change requires a fresh confirmation.
type DigestInput struct {
	OriginVersion                    int64
	ActiveVersionID                  string
	DraftID, DraftStatus, DraftState string
	CaptureDraftID                   string
	VideoPlanID                      string
	VideoGeneration                  int
}

// ConfirmationDigest returns the stable SHA-256 confirmation token for a promotion.
func ConfirmationDigest(in DigestInput) string {
	conflict := ""
	if in.DraftID != "" {
		conflict = strings.Join([]string{in.DraftID, in.DraftStatus, in.DraftState}, "|")
	}
	video := ""
	if in.VideoPlanID != "" {
		video = fmt.Sprintf("%s|%d", in.VideoPlanID, in.VideoGeneration)
	}
	canonical := fmt.Sprintf("%d|%s|%s|%s|%s", in.OriginVersion, in.ActiveVersionID, conflict, in.CaptureDraftID, video)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
