package messaging

import "testing"

func TestTranscriptionQueueContractsIncludeRetryAndDLQ(t *testing.T) {
	contracts, err := TranscriptionQueueContracts("test-")
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 1 {
		t.Fatalf("got %d transcription queue contracts, want MSG01 only; MSG02/03 belong to task_05", len(contracts))
	}
	want := []string{"transcription.requested.v1"}
	for i, contract := range contracts {
		if contract.RoutingKey != want[i] || contract.Name[:5] != "test-" || !contract.Durable || contract.DLQName == "" || contract.RetryNames[0] == "" {
			t.Fatalf("incomplete queue contract: %+v", contract)
		}
	}
}
