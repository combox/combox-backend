package postgres

import (
	"context"
	"strings"
	"time"

	"combox-backend/internal/service/reports"
)

// ReportsRepository stores rows of migration 000047_reports (table reports).
type ReportsRepository struct {
	client *Client
}

func NewReportsRepository(client *Client) *ReportsRepository {
	return &ReportsRepository{client: client}
}

// Create inserts one report row and returns it.
func (r *ReportsRepository) Create(ctx context.Context, reporterID, targetType, targetID, reason string) (reports.Entry, error) {
	const query = `
		INSERT INTO reports (reporter_id, target_type, target_id, reason)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text, reporter_id::text, target_type, target_id, reason, created_at
	`
	var (
		entry     reports.Entry
		createdAt time.Time
	)
	err := r.client.pool.QueryRow(
		ctx, query,
		strings.TrimSpace(reporterID),
		strings.ToLower(strings.TrimSpace(targetType)),
		strings.TrimSpace(targetID),
		strings.TrimSpace(reason),
	).Scan(&entry.ID, &entry.ReporterID, &entry.TargetType, &entry.TargetID, &entry.Reason, &createdAt)
	if err != nil {
		return reports.Entry{}, err
	}
	entry.CreatedAt = createdAt.UTC()
	return entry, nil
}
