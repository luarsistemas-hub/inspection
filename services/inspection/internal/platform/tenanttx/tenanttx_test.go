package tenanttx

import (
	"context"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"testing"
)

func TestTenantTxRejectsUnsafeInputsUT006(t *testing.T) {
	if err := (Runner{}).Within(context.Background(), uuid.Nil, func(*gorm.DB) error { return nil }); err == nil {
		t.Fatal("missing DB accepted")
	}
}
