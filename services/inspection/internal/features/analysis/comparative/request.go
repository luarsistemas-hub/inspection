// Package comparative builds provider-neutral evidence with explicit lineage.
package comparative

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/llm"
)

type Evidence struct {
	ID                       identity.ID
	Source, PairID, Position string
	Digest, DataURL          string
}

// Build validates and deterministically orders current and origin evidence.
func Build(current, origin []Evidence) ([]llm.NormalizedImage, string, error) {
	if len(current) == 0 {
		return nil, "", fmt.Errorf("comparative analysis requires current evidence")
	}
	if len(origin) == 0 {
		return nil, "", fmt.Errorf("comparative analysis requires origin evidence")
	}
	all := append(append([]Evidence{}, origin...), current...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].PairID != all[j].PairID {
			return all[i].PairID < all[j].PairID
		}
		if all[i].PairID != "" && all[i].Source != all[j].Source {
			return all[i].Source == "ORIGIN"
		}
		if all[i].Position != all[j].Position {
			return all[i].Position < all[j].Position
		}
		if all[i].Source != all[j].Source {
			return all[i].Source < all[j].Source
		}
		return all[i].ID.String() < all[j].ID.String()
	})
	seen := map[identity.ID]bool{}
	sum := sha256.New()
	images := make([]llm.NormalizedImage, 0, len(all))
	for _, item := range all {
		if item.ID == (identity.ID{}) || item.Source == "" || item.Digest == "" || item.DataURL == "" {
			return nil, "", fmt.Errorf("comparative evidence is incomplete")
		}
		if seen[item.ID] {
			return nil, "", fmt.Errorf("comparative evidence id is duplicated")
		}
		seen[item.ID] = true
		images = append(images, llm.NormalizedImage{EvidenceID: item.ID.String(), Source: item.Source, PairID: item.PairID, Position: item.Position, Digest: item.Digest, DataURL: item.DataURL})
		fmt.Fprintf(sum, "%s\x00%s\x00%s\x00%s\x00%s\x00", item.Source, item.ID, item.Digest, item.PairID, item.Position)
	}
	return images, hex.EncodeToString(sum.Sum(nil)), nil
}
