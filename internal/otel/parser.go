package otel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// flexInt64 handles JSON values that can be either a number or a string.
// OTEL protocol encodes large integers (timeUnixNano, asInt) as strings.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	// Try as number first
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexInt64(n)
		return nil
	}
	// Try as string
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("flexInt64: cannot unmarshal %s", string(b))
	}
	if s == "" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("flexInt64: parse %q: %w", s, err)
	}
	*f = flexInt64(n)
	return nil
}

// MetricRecord holds aggregated token usage for one model at one timestamp.
type MetricRecord struct {
	Timestamp           time.Time
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// Parse extracts MetricRecord entries from an OTEL JSON metrics payload.
func Parse(payload []byte) ([]MetricRecord, error) {
	var root otelPayload
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("unmarshal otel payload: %w", err)
	}

	type groupKey struct {
		nanos int64
		model string
	}
	groups := make(map[groupKey]*MetricRecord)

	for _, rm := range root.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "claude_code.token.usage" {
					continue
				}
				if m.Sum == nil {
					continue
				}
				for _, dp := range m.Sum.DataPoints {
					model := ""
					tokenType := ""
					for _, attr := range dp.Attributes {
						switch attr.Key {
						case "model":
							model = attr.Value.StringValue
						case "type":
							tokenType = attr.Value.StringValue
						}
					}
					if model == "" || tokenType == "" {
						continue
					}

					model = NormalizeModelName(model)

					value := int64(dp.AsInt)
					if value == 0 {
						value = int64(dp.AsDouble)
					}

					nanos := int64(dp.TimeUnixNano)
					key := groupKey{nanos: nanos, model: model}

					rec, ok := groups[key]
					if !ok {
						rec = &MetricRecord{
							Timestamp: time.Unix(0, nanos),
							Model:     model,
						}
						groups[key] = rec
					}

					switch tokenType {
					case "input":
						rec.InputTokens += value
					case "output":
						rec.OutputTokens += value
					case "cacheRead":
						rec.CacheReadTokens += value
					case "cacheCreation":
						rec.CacheCreationTokens += value
					}
				}
			}
		}
	}

	records := make([]MetricRecord, 0, len(groups))
	for _, rec := range groups {
		records = append(records, *rec)
	}
	return records, nil
}

// modelPattern matches model identifiers like "claude-opus-4-6-v1" or
// "us.anthropic.claude-sonnet-4-5-20250115-v1:0" and captures family + version (major-minor only).
var modelPattern = regexp.MustCompile(`claude-(opus|sonnet|haiku)-(\d+-\d+)`)

// NormalizeModelName converts a raw model identifier to a short canonical form.
// Example: "us.anthropic.claude-opus-4-6-v1:0" → "opus-4-6"
func NormalizeModelName(raw string) string {
	raw = strings.TrimSpace(raw)
	m := modelPattern.FindStringSubmatch(raw)
	if m == nil {
		return raw
	}
	return m[1] + "-" + m[2]
}

// --- internal JSON structures matching OTEL metrics export schema ---

type otelPayload struct {
	ResourceMetrics []resourceMetric `json:"resourceMetrics"`
}

type resourceMetric struct {
	ScopeMetrics []scopeMetric `json:"scopeMetrics"`
}

type scopeMetric struct {
	Metrics []metric `json:"metrics"`
}

type metric struct {
	Name string     `json:"name"`
	Sum  *sumMetric `json:"sum"`
}

type sumMetric struct {
	DataPoints []dataPoint `json:"dataPoints"`
}

type dataPoint struct {
	Attributes   []attribute `json:"attributes"`
	TimeUnixNano flexInt64   `json:"timeUnixNano"`
	AsInt        flexInt64   `json:"asInt"`
	AsDouble     float64     `json:"asDouble"`
}

type attribute struct {
	Key   string         `json:"key"`
	Value attributeValue `json:"value"`
}

type attributeValue struct {
	StringValue string `json:"stringValue"`
}
