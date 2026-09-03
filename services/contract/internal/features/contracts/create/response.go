package create

import (
	"encoding/json"
	"net/http"

	"inspection/services/contract/internal/features/contracts/create/internal"
)

type createContractResponse struct {
	ContractID string `json:"contractId"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func newCreateContractResponse(output internal.CreateContractOutput) createContractResponse {
	return createContractResponse{ContractID: output.ContractID}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
