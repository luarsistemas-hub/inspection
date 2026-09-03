package create

import "inspection/services/contract/internal/features/contracts/create/internal"

type createContractRequest struct {
	AccountID string   `json:"accountId"`
	ClientID  string   `json:"clientId"`
	TenantID  string   `json:"tenantId"`
	ProductID string   `json:"productId"`
	Photos    []string `json:"photos"`
}

func (r createContractRequest) toInput() internal.CreateContractInput {
	return internal.CreateContractInput{
		AccountID: r.AccountID,
		ClientID:  r.ClientID,
		TenantID:  r.TenantID,
		ProductID: r.ProductID,
	}
}
