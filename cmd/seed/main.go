package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/eidrogo/work-management-api/internal/config"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/database"
	"github.com/eidrogo/work-management-api/internal/seed"
)

func main() {
	if err := run(); err != nil {
		log.Printf("seed failed: %v", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.LoadCommand(nil)
	if err != nil {
		return err
	}
	db, err := database.Open(context.Background(), cfg.Database.URL, "work-management-seed", cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer db.Close()
	summary, err := seed.Apply(context.Background(), db, clock.System{})
	if err != nil {
		return err
	}
	fmt.Printf("seed complete: %d users and %d sample work items ensured\n", summary.Users, summary.WorkItems)
	return nil
}
