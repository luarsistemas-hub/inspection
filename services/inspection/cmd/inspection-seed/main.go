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
	tokenIssuer := os.Getenv("INSPECTION_OIDC_TOKEN_URL")
	if tokenIssuer == "" {
		tokenIssuer = issuer
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
	mediaKind := envOr("INSPECTION_QA_MEDIA_KIND", "PHOTO")
	scenario := os.Getenv("INSPECTION_QA_SCENARIO")
	if scenario != "" && scenario != "ORIGIN_VIDEO" && scenario != "FIXED_ORIGIN_VIDEO" {
		log.Fatal("INSPECTION_QA_SCENARIO must be ORIGIN_VIDEO or FIXED_ORIGIN_VIDEO")
	}
	if scenario != "" && os.Getenv("INSPECTION_ENV") != "local" {
		log.Fatal("video origin QA scenarios can only run in the local environment")
	}
	if scenario != "" {
		mediaKind = "VIDEO"
	}
	enableVideoAdmission := os.Getenv("INSPECTION_QA_ENABLE_VIDEO_ADMISSION") == "true"
	if enableVideoAdmission && os.Getenv("INSPECTION_ENV") != "local" {
		log.Fatal("QA video admission can only be enabled in the local environment")
	}
	url, err := seedqa.Setup(ctx, db, issuer, captureBaseURL, mediaKind, enableVideoAdmission)
	if errors.Is(err, seedqa.ErrLocalOnboardingMissing) {
		apiURL := os.Getenv("INSPECTION_API_URL")
		if apiURL == "" {
			apiURL = fmt.Sprintf("http://localhost:%s/graphql", envOr("INSPECTION_API_PORT", "8080"))
		}
		if bootstrapErr := bootstrapLocalAdmin(ctx, apiURL, tokenIssuer, issuer, envOr("INSPECTION_SEED_USERNAME", "admin"), envOr("INSPECTION_SEED_PASSWORD", "admin")); bootstrapErr != nil {
			log.Fatalf("local onboarding is missing and automatic bootstrap failed: %v", bootstrapErr)
		}
		url, err = seedqa.Setup(ctx, db, issuer, captureBaseURL, mediaKind, enableVideoAdmission)
	}
	if err != nil {
		log.Fatal(err)
	}
	if scenario == "ORIGIN_VIDEO" {
		url, err = seedqa.SetupOriginVideoInvite(ctx, db, issuer, captureBaseURL)
		if err != nil {
			log.Fatal(err)
		}
	}
	if scenario == "FIXED_ORIGIN_VIDEO" {
		if err := seedqa.SetupFixedOriginVideo(ctx, db, issuer, os.Getenv("INSPECTION_MINIO_ENDPOINT"), os.Getenv("INSPECTION_MINIO_ACCESS_KEY"), os.Getenv("INSPECTION_MINIO_SECRET_KEY"), os.Getenv("INSPECTION_MINIO_BUCKET"), os.Getenv("INSPECTION_MINIO_SECURE") == "true"); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("QA data created. Capture URL: %s\n", url)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
