// Package core aggregates actual provider usage without inventing estimates.
package core

import (
	"fmt"
	"time"
)

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

// CostComplete reports whether every matching call has a known cost outcome.
// Calls that failed without a reported cost do not make the known total partial.
func CostComplete(unknown, uncertain, unverified int64) bool {
	return unknown == 0 && uncertain == 0 && unverified == 0
}

// WindowCoverageComplete reports whether a filtered time window is covered by
// the cost provenance ledger and contains no unverified calls.
func WindowCoverageComplete(cutoff *time.Time, from time.Time, unverified int64) bool {
	return cutoff != nil && !from.Before(cutoff.UTC()) && unverified == 0
}

// InspectionCoverageComplete reports whether all calls for an inspection
// started after the ledger cutoff and none has unverified cost provenance.
func InspectionCoverageComplete(cutoff *time.Time, callsBeforeCutoff, unverified int64) bool {
	return cutoff != nil && callsBeforeCutoff == 0 && unverified == 0
}
