package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	seedqa "inspection/services/inspection/internal/features/development/seed_qa"
	"inspection/services/inspection/internal/platform/database"
)

func main() {
	databaseURL := os.Getenv("INSPECTION_MIGRATION_DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("INSPECTION_MIGRATION_DATABASE_URL is required")
	}
	issuer := os.Getenv("INSPECTION_OIDC_ISSUER")
	if issuer == "" {
		issuer = "http://localhost:8081/realms/inspection"
	}
	captureBaseURL := os.Getenv("NEXT_PUBLIC_CAPTURE_BASE_URL")
	if captureBaseURL == "" {
		captureBaseURL = "http://localhost:3003"
	}

	db, err := database.Open(databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	url, err := seedqa.Setup(ctx, db, issuer, captureBaseURL)
	if errors.Is(err, seedqa.ErrLocalOnboardingMissing) {
		apiURL := os.Getenv("INSPECTION_API_URL")
		if apiURL == "" {
			apiURL = fmt.Sprintf("http://localhost:%s/graphql", envOr("INSPECTION_API_PORT", "8080"))
		}
		if bootstrapErr := bootstrapLocalAdmin(ctx, apiURL, issuer, envOr("INSPECTION_SEED_USERNAME", "admin"), envOr("INSPECTION_SEED_PASSWORD", "admin")); bootstrapErr != nil {
			log.Fatalf("local onboarding is missing and automatic bootstrap failed: %v", bootstrapErr)
		}
		url, err = seedqa.Setup(ctx, db, issuer, captureBaseURL)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("QA data created. Capture URL: %s\n", url)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
