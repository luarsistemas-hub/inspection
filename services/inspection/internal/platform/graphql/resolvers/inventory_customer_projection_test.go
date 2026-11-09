package resolvers

import (
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"
)

func TestInventoryCustomerItemUT033UsesCustomerAllowlist(t *testing.T) {
	componentKey, confidence := "sala.sofa", 9100
	view := inventorycore.ItemView{Item: database.InventoryItem{
		ID: identity.NewID(), TenantID: identity.NewID(), Name: "Sofá", Description: "Descrição interna", ComponentKey: &componentKey,
		Suggested: json.RawMessage(`{"internal":"suggestion"}`), ConfidenceBps: &confidence, Decision: "PENDING",
	}}
	projected := inventoryCustomerItem(view)
	data, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Descrição interna", "sala.sofa", "internal", "9100", "PENDING"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("customer item leaked %q: %s", forbidden, data)
		}
	}
	if projected.Description != "" || projected.ComponentKey != nil || projected.Suggested != nil {
		t.Fatalf("customer projection exposed descriptive/review fields: %+v", projected)
	}
}
