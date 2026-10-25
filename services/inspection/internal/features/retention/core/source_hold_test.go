package core

import (
	"testing"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUnit_UT313_314_ContestationHoldLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"retention", "inspections", "media", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`CREATE TABLE retention.legal_holds(id text,tenant_id text,source_media_id text,inspection_id text,origin_version_id text,active numeric)`,
		`CREATE TABLE inspections.responsibilities(id text,tenant_id text,inspection_id text)`,
		`CREATE TABLE media.media_objects(id text,tenant_id text,responsibility_id text)`,
		`CREATE TABLE media.video_preparation_jobs(id text,tenant_id text,media_id text)`,
		`CREATE TABLE origins.origin_versions(id text,tenant_id text,video_preparation_id text)`,
		`CREATE TABLE origins.counterparty_invitations(id text,tenant_id text,origin_version_id text,response text,resolution text)`,
		`CREATE TABLE origins.origin_evidence(id text,tenant_id text,origin_version_id text,media_id text)`,
		`CREATE TABLE origins.inventory_item_evidence(id text,tenant_id text,item_id text,media_id text)`,
		`CREATE TABLE origins.inventory_environments(id text,tenant_id text,decision text)`,
		`CREATE TABLE origins.inventory_items(id text,tenant_id text,origin_version_id text,environment_id text,decision text,merged_into_id text)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, mediaID, versionID, invitationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id) VALUES(?,?)`, versionID, tenantID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO origins.origin_evidence(id,tenant_id,origin_version_id,media_id) VALUES(?,?,?,?)`, identity.NewID(), tenantID, versionID, mediaID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO origins.counterparty_invitations(id,tenant_id,origin_version_id,response,resolution) VALUES(?,?,?,?,?)`, invitationID, tenantID, versionID, "CONTESTED", "OPEN").Error; err != nil {
		t.Fatal(err)
	}
	held, err := EffectiveSourceHeld(db, tenantID, mediaID)
	if err != nil || !held {
		t.Fatalf("UT-313 open contestation hold = %v, %v; want held", held, err)
	}
	if err := db.Exec(`UPDATE origins.counterparty_invitations SET resolution='ACCEPTED' WHERE id=?`, invitationID).Error; err != nil {
		t.Fatal(err)
	}
	held, err = EffectiveSourceHeld(db, tenantID, mediaID)
	if err != nil || held {
		t.Fatalf("UT-314 resolved contestation hold = %v, %v; want released", held, err)
	}
}
