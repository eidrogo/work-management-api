package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"unicode/utf8"

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
	return executeReplay(context.Background(), store, os.Args[1:], id.NewUUID, os.Stdout)
}

type replayRepository interface {
	Replay(context.Context, notifications.ReplayRequest) (notifications.Delivery, error)
}

func executeReplay(ctx context.Context, repository replayRepository, args []string, newRequestID func() (string, error), stdout io.Writer) error {
	if repository == nil || newRequestID == nil || stdout == nil {
		return fmt.Errorf("replay dependencies are required")
	}
	if len(args) < 3 {
		return fmt.Errorf("usage: replay-notification <failed-notification-uuid> <operator-identity> <reason>")
	}
	notificationID := strings.TrimSpace(args[0])
	actorID := strings.TrimSpace(args[1])
	reason := strings.TrimSpace(strings.Join(args[2:], " "))
	if !id.IsUUID(notificationID) {
		return fmt.Errorf("notification ID must be a UUID")
	}
	if actorID == "" || utf8.RuneCountInString(actorID) > 200 {
		return fmt.Errorf("operator identity must contain between 1 and 200 characters")
	}
	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return fmt.Errorf("reason must contain between 1 and 1000 characters")
	}
	requestID, err := newRequestID()
	if err != nil {
		return fmt.Errorf("generate replay request ID: %w", err)
	}
	if !id.IsUUID(requestID) {
		return fmt.Errorf("generated request ID must be a UUID")
	}
	delivery, err := repository.Replay(ctx, notifications.ReplayRequest{
		NotificationID: notificationID,
		ActorID:        actorID,
		RequestID:      requestID,
		Channel:        notifications.ReplayChannelCLI,
		Reason:         reason,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "notification %s queued for delivery cycle %d (request %s)\n", delivery.ID, delivery.DeliveryCycle, requestID)
	return err
}
