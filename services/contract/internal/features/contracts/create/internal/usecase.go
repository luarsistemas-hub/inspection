package internal

import (
	"context"
	"errors"
	"fmt"

	"inspection/libs/identity"
)

type CreateContractInput struct {
	AccountID string
	ClientID  string
	TenantID  string
	ProductID string
}

type CreateContractOutput struct {
	ContractID string
}

// ValidationError marks request-data errors so the HTTP boundary can map them
// to a 400 response without coupling the use case to HTTP.
type ValidationError struct {
	err error
}

func (e ValidationError) Error() string {
	return e.err.Error()
}

func (e ValidationError) Unwrap() error {
	return e.err
}

func IsValidationError(err error) bool {
	var validationError ValidationError
	return errors.As(err, &validationError)
}

type CreateContractUseCase struct {
	repository Repository
}

func NewCreateContractUseCase(repository Repository) CreateContractUseCase {
	return CreateContractUseCase{repository: repository}
}

func (u CreateContractUseCase) Execute(ctx context.Context, input CreateContractInput) (CreateContractOutput, error) {
	productID, err := parseID("productId", input.ProductID)
	if err != nil {
		return CreateContractOutput{}, ValidationError{err: err}
	}
	accountID, err := parseID("accountId", input.AccountID)
	if err != nil {
		return CreateContractOutput{}, ValidationError{err: err}
	}
	clientID, err := parseID("clientId", input.ClientID)
	if err != nil {
		return CreateContractOutput{}, ValidationError{err: err}
	}
	tenantID, err := parseID("tenantId", input.TenantID)
	if err != nil {
		return CreateContractOutput{}, ValidationError{err: err}
	}

	contract, err := NewContract(productID, accountID, clientID, tenantID)
	if err != nil {
		return CreateContractOutput{}, ValidationError{err: fmt.Errorf("create contract entity: %w", err)}
	}
	if err := u.repository.Create(ctx, contract); err != nil {
		return CreateContractOutput{}, fmt.Errorf("persist contract: %w", err)
	}

	return CreateContractOutput{ContractID: contract.ContractID.String()}, nil
}

func parseID(field, value string) (identity.ID, error) {
	id, err := identity.ParseID(value)
	if err != nil {
		return identity.ID{}, fmt.Errorf("%s must be a valid UUID", field)
	}
	return id, nil
}
