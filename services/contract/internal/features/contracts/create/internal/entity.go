package internal

import (
	"errors"
	"time"

	"inspection/libs/identity"
)

// Contract is the domain entity used only by the create-contract feature.
type Contract struct {
	ContractID identity.ID `gorm:"column:contractid;primaryKey"`
	AccountID  identity.ID `gorm:"column:accountid"`
	ClientID   identity.ID `gorm:"column:clientid"`
	TenantID   identity.ID `gorm:"column:tenantid"`
	ProductID  identity.ID `gorm:"column:productid"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func NewContract(productID, accountID, clientID, tenantID identity.ID) (Contract, error) {
	if productID == (identity.ID{}) {
		return Contract{}, errors.New("productId is required")
	}
	if accountID == (identity.ID{}) {
		return Contract{}, errors.New("accountId is required")
	}
	if clientID == (identity.ID{}) {
		return Contract{}, errors.New("clientId is required")
	}
	if tenantID == (identity.ID{}) {
		return Contract{}, errors.New("tenantId is required")
	}

	now := time.Now().UTC()
	return Contract{
		ContractID: identity.NewID(),
		ProductID:  productID,
		AccountID:  accountID,
		ClientID:   clientID,
		TenantID:   tenantID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}
