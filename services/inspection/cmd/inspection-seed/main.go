package main

import (
	"context"
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
	url, err := seedqa.Setup(context.Background(), db, issuer, captureBaseURL)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("QA data created. Capture URL: %s\n", url)
}
