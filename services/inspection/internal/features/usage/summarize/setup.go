package summarize

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	usagecore "inspection/services/inspection/internal/features/usage/core"
	"inspection/services/inspection/internal/platform/database"
	"time"
)

func Query(ctx context.Context, db *gorm.DB, tenantID identity.ID, from, to time.Time) (usagecore.Summary, error) {
	var rows []database.UsageRecord
	if err := db.WithContext(ctx).Where("tenant_id=? AND created_at>=? AND created_at<?", tenantID, from, to).Find(&rows).Error; err != nil {
		return usagecore.Summary{}, err
	}
	records := make([]usagecore.Record, 0, len(rows))
	for _, row := range rows {
		records = append(records, usagecore.Record{Provider: row.Provider, Model: row.Model, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, Cost: row.Cost, LatencyMS: row.LatencyMS})
	}
	return usagecore.Summarize(records)
}
