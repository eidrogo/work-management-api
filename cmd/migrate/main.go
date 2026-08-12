package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/eidrogo/work-management-api/internal/config"
	"github.com/eidrogo/work-management-api/internal/migrations"
	"github.com/eidrogo/work-management-api/internal/platform/database"
)

func main() {
	if err := run(); err != nil {
		log.Printf("migration failed: %v", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.LoadCommand(nil)
	if err != nil {
		return err
	}
	db, err := database.Open(context.Background(), cfg.Database.URL, "work-management-migrate", cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	runner := migrations.Runner{DB: db, Dir: cfg.MigrationsDir}
	action := "up"
	if len(os.Args) > 1 {
		action = os.Args[1]
	}
	switch action {
	case "up":
		applied, err := runner.Up(context.Background())
		if err != nil {
			return err
		}
		for _, item := range applied {
			fmt.Printf("applied %06d_%s\n", item.Version, item.Name)
		}
		if len(applied) == 0 {
			fmt.Println("database is up to date")
		}
		return nil
	case "down":
		steps := 1
		if len(os.Args) > 2 {
			steps, err = strconv.Atoi(os.Args[2])
			if err != nil {
				return err
			}
		}
		_, err = runner.Down(context.Background(), steps)
		return err
	default:
		return fmt.Errorf("usage: migrate [up|down [steps]]")
	}
}
