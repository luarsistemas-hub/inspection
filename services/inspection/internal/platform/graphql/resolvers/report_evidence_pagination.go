package resolvers

import (
	"encoding/base64"
	"fmt"
	"strings"

	"inspection/libs/identity"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
)

func pageLimit(first *int) (int, error) {
	if first == nil {
		return 25, nil
	}
	if *first < 1 || *first > 100 {
		return 0, graphql1Error("first")
	}
	return *first, nil
}

func encodeEvidenceCursor(tenantID, resourceID identity.ID, evidenceID string) string {
	value := fmt.Sprintf("e1|%s|%s|%s", tenantID, resourceID, evidenceID)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeEvidenceCursor(value string, tenantID, resourceID identity.ID) (string, error) {
	if value == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 4 || parts[0] != "e1" || parts[1] != tenantID.String() || parts[2] != resourceID.String() || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("invalid evidence cursor")
	}
	return parts[3], nil
}

func sliceEvidencePage(items []*graphql1.ReportEvidence, first int, after string) ([]*graphql1.ReportEvidence, bool, error) {
	start := 0
	if after != "" {
		for i, item := range items {
			if item.ID == after {
				start = i + 1
				break
			}
		}
		if start == 0 {
			return nil, false, fmt.Errorf("cursor evidence not found")
		}
	}
	end := start + first
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], end < len(items), nil
}

func mustID(value string) identity.ID {
	id, _ := identity.ParseID(value)
	return id
}
