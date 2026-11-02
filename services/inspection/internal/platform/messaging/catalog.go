package messaging

import "strings"

// CatalogQueueContracts defines the durable queues that coordinate room
// catalogs with voice notes. They bind to the topic exchange with their own
// retry and dead-letter queues:
//
//   - transcription.completed.v1 (MSG02) and transcription.failed.v1 (MSG03)
//     re-evaluate the note manifest and the bounded wait of the note's room;
//   - inventory.job_requested.v1 (MSG05) re-evaluates readiness when visual
//     evidence or a terminal preparation failure arrives.
//
// Every consumer is idempotent: a redelivery re-evaluates the same state.
func CatalogQueueContracts(prefix ...string) ([]QueueContract, error) {
	queuePrefix := ""
	if len(prefix) > 0 {
		queuePrefix = strings.TrimSpace(prefix[0])
	}
	definitions := [][2]string{
		{"catalog-transcription-completed", "transcription.completed.v1"},
		{"catalog-transcription-failed", "transcription.failed.v1"},
		{"catalog-inventory-job-requested", "inventory.job_requested.v1"},
	}
	contracts := make([]QueueContract, 0, len(definitions))
	for _, definition := range definitions {
		contract, err := NewQueueContract(queuePrefix+definition[0], definition[1], 8)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, contract)
	}
	return contracts, nil
}
