package create

import (
	"fmt"

	"inspection/services/contract/internal/features/contracts/create/internal"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// Setup is the create-contract feature's only public API. It wires the
// feature-private persistence, use case and HTTP handler to the router.
func Setup(router chi.Router, db *gorm.DB) error {
	repository, err := internal.NewRepository(db)
	if err != nil {
		return fmt.Errorf("setup contract repository: %w", err)
	}

	h := newHandler(internal.NewCreateContractUseCase(repository))
	router.Post("/create-contract", h.handle)
	return nil
}
