package build_request

import (
	"strings"
	"testing"

	"inspection/libs/identity"
)

func TestSetupRequiresObjectStore(t *testing.T) {
	if _, err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing object store accepted")
	}
}

func TestAttentionItemsAreScopedToTheirOriginEvidence(t *testing.T) {
	first, second := identity.NewID(), identity.NewID()
	snapshot := referenceSnapshot{Items: []referenceEvidence{{MediaID: first, AttentionItems: []string{"Cafeteira"}}, {MediaID: second, AttentionItems: []string{"Torneira"}}}}
	prompt, err := addAttentionItemsToPrompt("base", snapshot.attentionItemsFor(second))
	if err != nil || !strings.Contains(prompt, `["Torneira"]`) || strings.Contains(prompt, "Cafeteira") {
		t.Fatalf("prompt=%q err=%v", prompt, err)
	}
	withoutItems, err := addAttentionItemsToPrompt("base", nil)
	if err != nil || withoutItems != "base" {
		t.Fatalf("empty items changed prompt=%q err=%v", withoutItems, err)
	}
}
