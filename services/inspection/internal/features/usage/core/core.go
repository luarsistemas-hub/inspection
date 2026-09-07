// Package core aggregates actual provider usage without inventing estimates.
package core

import "fmt"

type Record struct {
	Provider, Model           string
	InputTokens, OutputTokens *int64
	Cost                      *float64
	LatencyMS                 int64
}
type Summary struct {
	Requests                  int
	InputTokens, OutputTokens int64
	KnownCost                 float64
	CostKnown                 bool
	LatencyMS                 int64
}

func Summarize(records []Record) (Summary, error) {
	var summary Summary
	for _, record := range records {
		if record.Provider == "" || record.Model == "" || record.LatencyMS < 0 {
			return Summary{}, fmt.Errorf("invalid provider usage")
		}
		summary.Requests++
		summary.LatencyMS += record.LatencyMS
		if record.InputTokens != nil {
			summary.InputTokens += *record.InputTokens
		}
		if record.OutputTokens != nil {
			summary.OutputTokens += *record.OutputTokens
		}
		if record.Cost != nil {
			summary.KnownCost += *record.Cost
			summary.CostKnown = true
		}
	}
	return summary, nil
}
