package create

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"inspection/libs/identity"
	"inspection/services/contract/internal/features/contracts/create/internal"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

type repositoryStub struct {
	created internal.Contract
	err     error
}

func (s *repositoryStub) Create(_ context.Context, value internal.Contract) error {
	s.created = value
	return s.err
}

func TestCreateContract_CreatesContract(t *testing.T) {
	repository := &repositoryStub{}
	router := setupTestRouter(repository)

	body := validRequest(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/create-contract", bytes.NewReader(body))
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusCreated, response.Code)
	require.NotEqual(t, identity.ID{}, repository.created.ContractID)
	require.Equal(t, "application/json", response.Header().Get("Content-Type"))

	var output createContractResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&output))
	require.Equal(t, repository.created.ContractID.String(), output.ContractID)
}

func TestCreateContract_RejectsInvalidUUID(t *testing.T) {
	repository := &repositoryStub{}
	router := setupTestRouter(repository)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/create-contract", bytes.NewBufferString(`{"accountId":"invalid"}`))
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, identity.ID{}, repository.created.ContractID)
}

func TestCreateContract_ReturnsInternalErrorWhenStorageFails(t *testing.T) {
	repository := &repositoryStub{err: errors.New("database unavailable")}
	router := setupTestRouter(repository)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/create-contract", bytes.NewReader(validRequest(t)))
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code)
}

func TestSetup_RequiresDatabase(t *testing.T) {
	err := Setup(chi.NewRouter(), nil)

	require.EqualError(t, err, "setup contract repository: database is required")
}

func setupTestRouter(repository internal.Repository) *chi.Mux {
	router := chi.NewRouter()
	h := newHandler(internal.NewCreateContractUseCase(repository))
	router.Post("/create-contract", h.handle)
	return router
}

func validRequest(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(createContractRequest{
		AccountID: identity.NewID().String(),
		ClientID:  identity.NewID().String(),
		TenantID:  identity.NewID().String(),
		ProductID: identity.NewID().String(),
	})
	require.NoError(t, err)
	return body
}
