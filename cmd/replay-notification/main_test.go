package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func TestExecuteReplay(t *testing.T) {
	t.Parallel()
	notificationID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	requestID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	tests := []struct {
		name       string
		args       []string
		requestID  string
		wantError  string
		wantReason string
	}{
		{name: "usage", args: nil, requestID: requestID, wantError: "usage:"},
		{name: "bad notification uuid", args: []string{"not-a-uuid", "operator", "reason"}, requestID: requestID, wantError: "UUID"},
		{name: "missing actor", args: []string{notificationID, " ", "reason"}, requestID: requestID, wantError: "operator"},
		{name: "missing reason", args: []string{notificationID, "operator", " "}, requestID: requestID, wantError: "reason"},
		{name: "invalid generated request id", args: []string{notificationID, "operator", "reason"}, requestID: "not-a-uuid", wantError: "request ID"},
		{name: "success joins reason", args: []string{notificationID, "operator@example.test", "provider", "issue", "resolved"}, requestID: requestID, wantReason: "provider issue resolved"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeReplayRepository{delivery: notifications.Delivery{ID: notificationID, DeliveryCycle: 2}}
			var output bytes.Buffer
			err := executeReplay(context.Background(), repository, test.args, func() (string, error) { return test.requestID, nil }, &output)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if repository.request.NotificationID != notificationID || repository.request.ActorID != "operator@example.test" || repository.request.Channel != notifications.ReplayChannelCLI || repository.request.RequestID != requestID || repository.request.Reason != test.wantReason {
				t.Fatalf("request=%#v", repository.request)
			}
			for _, value := range []string{notificationID, "cycle 2", requestID} {
				if !strings.Contains(output.String(), value) {
					t.Fatalf("output=%q missing=%q", output.String(), value)
				}
			}
		})
	}
}

type fakeReplayRepository struct {
	request  notifications.ReplayRequest
	delivery notifications.Delivery
}

func (repository *fakeReplayRepository) Replay(_ context.Context, request notifications.ReplayRequest) (notifications.Delivery, error) {
	repository.request = request
	return repository.delivery, nil
}
