package store

import (
	"context"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// Usage totals what each provider did over the last `days` days. Turns that
// reported no token counts still count as turns: a provider that does not
// report usage should not look idle.
func (s *Store) Usage(ctx context.Context, days int) (domain.UsageReport, error) {
	if days <= 0 || days > 365 {
		days = 7
	}
	report := domain.UsageReport{Days: days, Providers: []domain.ProviderUsage{}}
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339Nano)

	rows, err := s.db.QueryContext(ctx, `
SELECT r.provider, COUNT(*), COALESCE(SUM(r.input_tokens), 0), COALESCE(SUM(r.output_tokens), 0),
       COUNT(DISTINCT t.conversation_id)
FROM chat_responses r
JOIN chat_turns t ON t.id = r.turn_id
WHERE r.updated_at >= ? AND r.status NOT IN (?, ?)
GROUP BY r.provider
ORDER BY SUM(r.input_tokens) + SUM(r.output_tokens) DESC, r.provider`,
		since, domain.StatusQueued, domain.StatusRunning)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.ProviderUsage
		if err := rows.Scan(&item.Provider, &item.Turns,
			&item.InputTokens, &item.OutputTokens, &item.Cards); err != nil {
			return report, err
		}
		report.Providers = append(report.Providers, item)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}

	// The provider's own allowance report rides along, so one panel can answer
	// both "what did I spend" and "how much is left".
	quota, err := s.ProviderQuota(ctx)
	if err != nil {
		return report, err
	}
	for index := range report.Providers {
		if item, known := quota[report.Providers[index].Provider]; known {
			value := item
			report.Providers[index].Quota = &value
		}
	}
	return report, nil
}
