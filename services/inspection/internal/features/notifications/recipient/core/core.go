// Package core contains recipient notification safety and idempotency rules.
package core

import "sort"

type Recipient struct {
	MembershipID, Channel, Destination string
	Verified, Selected, Active         bool
}
type Notification struct {
	MembershipID, EventID, Kind, Title, Body string
	CreatedAt                                int64
	ReadAt                                   *int64
}

func FanOut(eventID, kind, title, body string, recipients []Recipient) []Recipient {
	seen := map[string]bool{}
	out := make([]Recipient, 0, len(recipients))
	for _, r := range recipients {
		if !r.Active || !r.Verified || !r.Selected || r.MembershipID == "" {
			continue
		}
		key := r.MembershipID + "\x00" + eventID + "\x00" + kind
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MembershipID == out[j].MembershipID {
			return out[i].Channel < out[j].Channel
		}
		return out[i].MembershipID < out[j].MembershipID
	})
	return out
}

func MarkRead(n Notification, currentMembership string, now int64) (Notification, error) {
	if n.MembershipID != currentMembership {
		return Notification{}, errNotFound{}
	}
	if n.ReadAt == nil {
		n.ReadAt = &now
	}
	return n, nil
}

type errNotFound struct{}

func (errNotFound) Error() string { return "notification not found" }
