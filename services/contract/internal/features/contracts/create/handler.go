package create

import (
	"context"
	"encoding/json"
	"net/http"

	"inspection/services/contract/internal/features/contracts/create/internal"
)

type createContractUseCase interface {
	Execute(context.Context, internal.CreateContractInput) (internal.CreateContractOutput, error)
}

type handler struct {
	useCase createContractUseCase
}

func newHandler(useCase createContractUseCase) handler {
	return handler{useCase: useCase}
}

func (h handler) handle(w http.ResponseWriter, r *http.Request) {
	var request createContractRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	output, err := h.useCase.Execute(r.Context(), request.toInput())
	if err != nil {
		if internal.IsValidationError(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create contract")
		return
	}

	writeJSON(w, http.StatusCreated, newCreateContractResponse(output))
}
