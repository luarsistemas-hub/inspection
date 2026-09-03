package main

import (
	"inspection/services/contract/internal/features/contracts/create"
	"inspection/services/contract/internal/platform/config"
	"log"
	"net/http"

	"fmt"

	"inspection/services/contract/docs"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// @title			Contract Service API
// @version		1.0
// @description	Contract Service API
// @host			localhost:8000
// @BasePath		/
// @schemes		http
// @contact.name	API Support
// @contact.url	http://www.swagger.io/support
// @contact.email	support@swagger.io
// @license.name	Apache 2.0
// @license.url	http://www.apache.org/licenses/LICENSE-2.0.html
func main() {
	cfg, err := config.Load(".")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := ensureDatabaseExists(cfg); err != nil {
		log.Fatalf("Failed to ensure database exists: %v", err)
	}

	databaseURL := fmt.Sprintf("%s://%s:%s@%s:%s/%s?sslmode=disable", cfg.DBDriver, cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.StripSlashes)
	router.Use(middleware.NoCache)
	router.Use(middleware.Compress(5))
	router.Use(middleware.SetHeader("Content-Type", "application/json"))
	if err := create.Setup(router, db); err != nil {
		log.Fatalf("Failed to setup create contract feature: %v", err)
	}

	docs.SwaggerInfo.Host = "localhost:" + cfg.WebServerPort
	router.Get("/docs/*", httpSwagger.Handler(httpSwagger.URL("http://localhost:"+cfg.WebServerPort+"/docs/doc.json")))
	if err := http.ListenAndServe(":"+cfg.WebServerPort, router); err != nil {
		log.Fatalf("Contract service stopped: %v", err)
	}
}

// ensureDatabaseExists creates cfg.DBName on the target Postgres server when
// it is missing. Postgres has no CREATE DATABASE IF NOT EXISTS, so this
// connects to the maintenance "postgres" database to check first.
func ensureDatabaseExists(cfg *config.Config) error {
	adminURL := fmt.Sprintf("%s://%s:%s@%s:%s/postgres?sslmode=disable", cfg.DBDriver, cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort)

	adminDB, err := gorm.Open(postgres.Open(adminURL), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("connect to maintenance database: %w", err)
	}
	sqlDB, err := adminDB.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	var exists bool
	if err := adminDB.Raw("SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = ?)", cfg.DBName).Scan(&exists).Error; err != nil {
		return fmt.Errorf("check database existence: %w", err)
	}
	if exists {
		return nil
	}

	if err := adminDB.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, cfg.DBName)).Error; err != nil {
		return fmt.Errorf("create database %q: %w", cfg.DBName, err)
	}
	return nil
}
