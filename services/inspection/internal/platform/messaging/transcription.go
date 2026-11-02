package messaging

import "strings"

// TranscriptionQueueContracts defines the dedicated queues for transcription
// requests and their durable completion/failure notifications.
func TranscriptionQueueContracts(prefix ...string) ([]QueueContract, error) {
	queuePrefix := ""
	if len(prefix) > 0 {
		queuePrefix = strings.TrimSpace(prefix[0])
	}
	definitions := [][2]string{{"inspection-transcription-requested", "transcription.requested.v1"}}
	contracts := make([]QueueContract, 0, len(definitions))
	for _, definition := range definitions {
		contract, err := NewQueueContract(queuePrefix+definition[0], definition[1], 1)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, contract)
	}
	return contracts, nil
}
