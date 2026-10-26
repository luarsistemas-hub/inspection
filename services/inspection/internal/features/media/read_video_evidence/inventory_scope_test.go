package read_video_evidence

import (
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
)

func TestInventoryFrameVisibleByRole(t *testing.T) {
	active, draft := identity.NewID(), identity.NewID()
	origin := database.Origin{ActiveVersionID: &active}
	activeVersion := database.OriginVersion{ID: active, Status: "ACTIVE"}
	draftVersion := database.OriginVersion{ID: draft, Status: "DRAFT"}
	superseded := database.OriginVersion{ID: identity.NewID(), Status: "SUPERSEDED"}
	cases := []struct {
		name      string
		roles     []string
		version   database.OriginVersion
		decisions []string
		want      bool
	}{
		{"reviewer reads a draft", []string{auth.Employee}, draftVersion, []string{"PENDING"}, true},
		{"manager reads a superseded version", []string{auth.Manager}, superseded, []string{"REJECTED"}, true},
		{"viewer reads an activated version", []string{auth.Viewer}, activeVersion, []string{"PENDING"}, true},
		{"viewer cannot read a draft", []string{auth.Viewer}, draftVersion, []string{"ACCEPTED"}, false},
		{"customer reads an included active item", []string{auth.CustomerViewer}, activeVersion, []string{"EDITED"}, true},
		{"customer cannot read a rejected item", []string{auth.CustomerViewer}, activeVersion, []string{"REJECTED"}, false},
		{"customer cannot read a superseded version", []string{auth.CustomerViewer}, superseded, []string{"ACCEPTED"}, false},
		{"auditor has no frame access", []string{auth.Auditor}, activeVersion, []string{"ACCEPTED"}, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := inventoryFrameVisible(item.roles, item.version, origin, item.decisions); got != item.want {
				t.Fatalf("inventoryFrameVisible=%v, want %v", got, item.want)
			}
		})
	}
}

func TestInventoryFrameVisibleWithoutActiveVersion(t *testing.T) {
	version := database.OriginVersion{ID: identity.NewID(), Status: "ACTIVE"}
	if inventoryFrameVisible([]string{auth.CustomerViewer}, version, database.Origin{}, []string{"ACCEPTED"}) {
		t.Fatal("customer read a frame of an origin without an active version")
	}
}
