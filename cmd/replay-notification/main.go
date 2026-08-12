package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/eidrogo/work-management-api/internal/config"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/platform/database"
	"github.com/eidrogo/work-management-api/internal/platform/id"
)

func main() {
	if err := run(); err != nil {
		log.Printf("notification replay failed: %v", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 || !id.IsUUID(os.Args[1]) {
		return fmt.Errorf("usage: replay-notification <failed-notification-uuid>")
	}
	cfg, err := config.LoadCommand(nil)
	if err != nil {
		return err
	}
	db, err := database.Open(context.Background(), cfg.Database.URL, "work-management-replay", cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	store, err := notifications.NewStore(db)
	if err != nil {
		return err
	}
	delivery, err := store.Replay(context.Background(), os.Args[1], time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Printf("notification %s queued for delivery cycle %d\n", delivery.ID, delivery.DeliveryCycle)
	return nil
}
