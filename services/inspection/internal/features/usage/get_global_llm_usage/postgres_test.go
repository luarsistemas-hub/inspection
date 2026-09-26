package get_global_llm_usage

import (
	"context"
	"os"
	"testing"

	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestHandleRunsLedgerFunctionsOnPostgres guards the SQL placeholders against
// the usage read functions' signatures, which only PostgreSQL can validate.
func TestHandleRunsLedgerFunctionsOnPostgres(t *testing.T) {
	dsn := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL admin DSN is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := (database.Migrator{DB: db}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, q := range []Query{{}, {Mode: "mock", State: "FINISHED", CostState: "missing", First: 1}} {
		if _, err := handle(context.Background(), db, q); err != nil {
			t.Fatalf("handle(%+v): %v", q, err)
		}
	}
}
