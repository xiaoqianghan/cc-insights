package stats

import (
	"fmt"
	"strings"
	"time"
)

// ANSI escape codes
const (
	bold   = "\033[1m"
	reset  = "\033[0m"
	green  = "\033[32m"
	red    = "\033[31m"
	dim    = "\033[2m"
	cyan   = "\033[36m"
	yellow = "\033[33m"
)

// Render prints PeriodStats to stdout with ANSI colors.
func Render(stats *PeriodStats) {
	start := parseDate(stats.StartDate)
	end := parseDate(stats.EndDate)

	header := fmt.Sprintf("Claude Code Usage (%s - %s)", formatDateShort(start), formatDateShort(end))
	line := strings.Repeat("─", max(60-len(header)-6, 4))
	fmt.Printf("\n%s─── %s %s%s\n\n", bold, header, line, reset)

	// Summary
	totalTokens := stats.InputTokens + stats.OutputTokens + stats.CacheReadTokens + stats.CacheCreationTokens
	fmt.Printf("  Requests:  %-12s Cost: %s$%.2f%s\n", formatNumber(stats.TotalRequests), green, stats.TotalCost, reset)
	fmt.Printf("  Tokens:    %-12s Cache Hit: %.1f%%\n", formatNumber(totalTokens), stats.CacheHitRate)

	// Trend vs previous period
	if stats.PrevPeriodCost > 0 {
		pctChange := (stats.TotalCost - stats.PrevPeriodCost) / stats.PrevPeriodCost * 100
		arrow, color := trendArrowColor(pctChange)
		fmt.Printf("  Trend:     %s%s %+.1f%% vs prev %s%s\n", color, arrow, pctChange, stats.Label, reset)
	}

	// Model breakdown
	if len(stats.Models) > 0 {
		fmt.Printf("\n  %sModel Breakdown:%s\n", bold, reset)
		fmt.Printf("  %-16s %8s %11s %9s %6s %s\n", "MODEL", "REQUESTS", "TOKENS", "COST", "SHARE", "")

		for _, m := range stats.Models {
			mTokens := m.InputTokens + m.OutputTokens + m.CacheReadTokens + m.CacheCreationTokens
			bar := shareBar(m.CostShare, 10)
			fmt.Printf("  %-16s %8s %11s %s%8s%s %5.1f%% %s\n",
				m.Model,
				formatNumber(m.Requests),
				formatNumber(mTokens),
				green, formatDollar(m.Cost), reset,
				m.CostShare,
				bar,
			)
		}
	}

	// Peak hours
	if len(stats.PeakHours) > 0 {
		fmt.Printf("\n  %sPeak Hours:%s ", bold, reset)
		for i, h := range stats.PeakHours {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%02d:00-%02d:00 (%d req)", h.Hour, h.Hour+1, h.Count)
		}
		fmt.Println()
	}

	// Daily
	if len(stats.Daily) > 0 {
		fmt.Printf("\n  %sDaily:%s\n", bold, reset)
		fmt.Printf("  %-12s %5s %9s %s\n", "DATE", "REQ", "COST", "TREND")

		avgCost := float64(0)
		if len(stats.Daily) > 0 {
			avgCost = stats.TotalCost / float64(len(stats.Daily))
		}

		for _, d := range stats.Daily {
			dt := parseDate(d.Date)
			dateStr := formatDateShort(dt)
			trendStr := ""
			if avgCost > 0 {
				pct := (d.Cost - avgCost) / avgCost * 100
				arrow, color := trendArrowColor(pct)
				trendStr = fmt.Sprintf("%s%s %+.0f%%%s", color, arrow, pct, reset)
			}
			fmt.Printf("  %-12s %5s %s%8s%s   %s\n",
				dateStr,
				formatNumber(d.Requests),
				green, formatDollar(d.Cost), reset,
				trendStr,
			)
		}
	}

	fmt.Println()
}

func parseDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func formatDateShort(t time.Time) string {
	return t.Format("Jan 02")
}

func formatNumber(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", n/1_000_000, (n%1_000_000)/1000, n%1000)
}

func formatDollar(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

func trendArrowColor(pct float64) (string, string) {
	if pct > 0 {
		return "▲", green
	}
	if pct < 0 {
		return "▼", red
	}
	return "─", dim
}

// shareBar renders a proportional bar using block characters.
// maxWidth is the number of character cells for the bar at 100%.
func shareBar(pct float64, maxWidth int) string {
	// Scale to half-blocks: each character is 2 units
	units := int(pct / 100 * float64(maxWidth) * 2)
	full := units / 2
	half := units % 2

	var b strings.Builder
	for i := 0; i < full; i++ {
		b.WriteRune('█')
	}
	if half > 0 {
		b.WriteRune('▌')
	}
	// If nothing, show a thin mark for non-zero shares
	if full == 0 && half == 0 && pct > 0 {
		b.WriteRune('▏')
	}
	return b.String()
}
