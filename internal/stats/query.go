package stats

import (
	"database/sql"
	"time"

	"github.com/xiaoqianghan/cc-insights/internal/config"
)

// PeriodStats holds aggregated usage statistics for a time period.
type PeriodStats struct {
	Label               string
	Days                int
	StartDate           string
	EndDate             string
	TotalRequests       int64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	TotalCost           float64
	CacheHitRate        float64 // percentage
	Models              []ModelStats
	PeakHours           []HourCount // top 3 hours
	Daily               []DayStats
	PrevPeriodCost      float64 // for trend comparison
}

// ModelStats holds per-model usage statistics.
type ModelStats struct {
	Model               string
	Requests            int64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	Cost                float64
	CostShare           float64 // percentage of total
}

// HourCount holds request count for a specific hour.
type HourCount struct {
	Hour  int
	Count int64
}

// DayStats holds per-day usage statistics.
type DayStats struct {
	Date     string
	Requests int64
	Cost     float64
}

func computeCost(input, output, cacheRead, cacheCreation int64, pricing map[string]config.ModelPricing, model string) float64 {
	p, ok := pricing[model]
	if !ok {
		return 0
	}
	return float64(input)/1e6*p.Input +
		float64(output)/1e6*p.Output +
		float64(cacheRead)/1e6*p.CacheRead +
		float64(cacheCreation)/1e6*p.CacheWrite
}

// Query fetches aggregated usage statistics for the given number of days.
func Query(db *sql.DB, days int, label string, pricing map[string]config.ModelPricing) (*PeriodStats, error) {
	now := time.Now()
	startDate := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	endDate := now.Format("2006-01-02")

	ps := &PeriodStats{
		Label:     label,
		Days:      days,
		StartDate: startDate,
		EndDate:   endDate,
	}

	// Query totals
	err := db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0)
		 FROM metrics WHERE date >= ?`, startDate,
	).Scan(&ps.TotalRequests, &ps.InputTokens, &ps.OutputTokens, &ps.CacheReadTokens, &ps.CacheCreationTokens)
	if err != nil {
		return nil, err
	}

	// Cache hit rate
	denom := ps.InputTokens + ps.CacheReadTokens
	if denom > 0 {
		ps.CacheHitRate = float64(ps.CacheReadTokens) / float64(denom) * 100
	}

	// Query per-model
	rows, err := db.Query(
		`SELECT model, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0)
		 FROM metrics WHERE date >= ?
		 GROUP BY model
		 ORDER BY (COALESCE(SUM(input_tokens),0)+COALESCE(SUM(output_tokens),0)+COALESCE(SUM(cache_read_tokens),0)+COALESCE(SUM(cache_creation_tokens),0)) DESC`,
		startDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var totalCost float64
	for rows.Next() {
		var ms ModelStats
		if err := rows.Scan(&ms.Model, &ms.Requests, &ms.InputTokens, &ms.OutputTokens, &ms.CacheReadTokens, &ms.CacheCreationTokens); err != nil {
			return nil, err
		}
		ms.Cost = computeCost(ms.InputTokens, ms.OutputTokens, ms.CacheReadTokens, ms.CacheCreationTokens, pricing, ms.Model)
		totalCost += ms.Cost
		ps.Models = append(ps.Models, ms)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ps.TotalCost = totalCost

	// Compute cost share percentages
	for i := range ps.Models {
		if totalCost > 0 {
			ps.Models[i].CostShare = ps.Models[i].Cost / totalCost * 100
		}
	}

	// Query peak hours
	hourRows, err := db.Query(
		`SELECT hour, COUNT(*) as cnt FROM metrics WHERE date >= ?
		 GROUP BY hour ORDER BY cnt DESC LIMIT 3`,
		startDate,
	)
	if err != nil {
		return nil, err
	}
	defer hourRows.Close()

	for hourRows.Next() {
		var hc HourCount
		if err := hourRows.Scan(&hc.Hour, &hc.Count); err != nil {
			return nil, err
		}
		ps.PeakHours = append(ps.PeakHours, hc)
	}
	if err := hourRows.Err(); err != nil {
		return nil, err
	}

	// Query daily
	dayRows, err := db.Query(
		`SELECT date, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0)
		 FROM metrics WHERE date >= ?
		 GROUP BY date ORDER BY date DESC`,
		startDate,
	)
	if err != nil {
		return nil, err
	}
	defer dayRows.Close()

	for dayRows.Next() {
		var ds DayStats
		var input, output, cacheRead, cacheCreation int64
		if err := dayRows.Scan(&ds.Date, &ds.Requests, &input, &output, &cacheRead, &cacheCreation); err != nil {
			return nil, err
		}
		// Compute daily cost by summing across all model pricings proportionally.
		// We use per-model daily query for accuracy, but for simplicity use aggregate cost.
		// Since we have total tokens per day but not per model, we estimate using average cost per token.
		if ps.TotalRequests > 0 && totalCost > 0 {
			totalTokens := ps.InputTokens + ps.OutputTokens + ps.CacheReadTokens + ps.CacheCreationTokens
			dayTokens := input + output + cacheRead + cacheCreation
			if totalTokens > 0 {
				ds.Cost = totalCost * float64(dayTokens) / float64(totalTokens)
			}
		}
		ps.Daily = append(ps.Daily, ds)
	}
	if err := dayRows.Err(); err != nil {
		return nil, err
	}

	// Previous period cost
	prevStart := now.AddDate(0, 0, -(2*days - 1)).Format("2006-01-02")
	prevEnd := now.AddDate(0, 0, -days).Format("2006-01-02")

	prevRows, err := db.Query(
		`SELECT model, COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0)
		 FROM metrics WHERE date >= ? AND date <= ?
		 GROUP BY model`,
		prevStart, prevEnd,
	)
	if err != nil {
		return nil, err
	}
	defer prevRows.Close()

	for prevRows.Next() {
		var model string
		var input, output, cacheRead, cacheCreation int64
		if err := prevRows.Scan(&model, &input, &output, &cacheRead, &cacheCreation); err != nil {
			return nil, err
		}
		ps.PrevPeriodCost += computeCost(input, output, cacheRead, cacheCreation, pricing, model)
	}
	if err := prevRows.Err(); err != nil {
		return nil, err
	}

	return ps, nil
}
