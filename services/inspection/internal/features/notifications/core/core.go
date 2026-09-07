// Package core owns critical-alert transition and recipient safety rules.
package core

import "sort"

type Recipient struct {
	ID, Channel, Destination     string
	Internal, Verified, Selected bool
}

// FirstCritical reports only a new transition. Returning recipients are always
// internal, verified, selected destinations; external participants cannot be
// made automatic recipients by this function.
func FirstCritical(previous, current string, recipients []Recipient) []Recipient {
	if current != "CRITICAL" || previous == "CRITICAL" {
		return nil
	}
	result := make([]Recipient, 0, len(recipients))
	seen := map[string]struct{}{}
	for _, recipient := range recipients {
		if !recipient.Internal || !recipient.Verified || !recipient.Selected || recipient.Destination == "" {
			continue
		}
		key := recipient.Channel + "\x00" + recipient.Destination
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, recipient)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Channel == result[j].Channel {
			return result[i].Destination < result[j].Destination
		}
		return result[i].Channel < result[j].Channel
	})
	return result
}
