package messaging

import "testing"

func TestCatalogQueueContractsCoverMSG02MSG03MSG05WithRetryAndDLQ(t *testing.T) {
	contracts, err := CatalogQueueContracts("test-")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"transcription.completed.v1", "transcription.failed.v1", "inventory.job_requested.v1"}
	if len(contracts) != len(want) {
		t.Fatalf("got %d catalog queue contracts, want %d", len(contracts), len(want))
	}
	names := map[string]bool{}
	for i, contract := range contracts {
		if contract.RoutingKey != want[i] || contract.Name[:5] != "test-" || !contract.Durable || contract.DLQName == "" || contract.RetryNames[0] == "" || contract.RetryNames[2] == "" {
			t.Fatalf("incomplete queue contract: %+v", contract)
		}
		if names[contract.Name] {
			t.Fatalf("duplicate queue %s", contract.Name)
		}
		names[contract.Name] = true
	}
	// The dedicated transcription request queue (MSG01) is not a catalog queue.
	transcription, err := TranscriptionQueueContracts("test-")
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range transcription {
		if names[contract.Name] {
			t.Fatalf("catalog queue %s collides with a transcription queue", contract.Name)
		}
	}
}
