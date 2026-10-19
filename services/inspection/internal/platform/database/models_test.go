package database

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestVideoFrameTimestampMatchesMigrationColumn(t *testing.T) {
	parsed, err := schema.Parse(&VideoFrame{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	field := parsed.LookUpField("PTSMicros")
	if field == nil || field.DBName != "pts_us" {
		t.Fatalf("VideoFrame.PTSMicros database column = %v, want pts_us", field)
	}
}
