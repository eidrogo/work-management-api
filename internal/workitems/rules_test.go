package workitems

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/eidrogo/work-management-api/internal/auth"
)

func TestValidateCreateInputEnforcesSMTPCompatibleTitleBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		title     string
		wantError bool
	}{
		{name: "200 runes", title: strings.Repeat("é", 200)},
		{name: "201 runes", title: strings.Repeat("é", 201), wantError: true},
		{name: "line feed", title: "Quarterly handoff\nURGENT", wantError: true},
		{name: "leading line feed", title: "\nQuarterly handoff", wantError: true},
		{name: "carriage return", title: "Quarterly handoff\rURGENT", wantError: true},
		{name: "CRLF", title: "Quarterly handoff\r\nURGENT", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateCreateInput(CreateInput{Title: test.title, IdempotencyKey: "title-boundary"})
			if !test.wantError {
				if err != nil || got.Title != test.title {
					t.Fatalf("input=%#v error=%v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v", err)
			}
			code, _, details := ErrorContract(err)
			if code != "INVALID_INPUT" || details["field"] != "title" || details["maximum"] != 200 {
				t.Fatalf("code=%q details=%v", code, details)
			}
		})
	}
}

func TestValidateCreateInputRejectsInvalidUTF8IdempotencyKey(t *testing.T) {
	t.Parallel()
	_, err := ValidateCreateInput(CreateInput{
		Title:          "Inspect warehouse",
		IdempotencyKey: "invalid-\xff-key",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error=%v", err)
	}
	code, message, details := ErrorContract(err)
	if code != "INVALID_INPUT" || message != "The Idempotency-Key header must contain valid UTF-8." || len(details) != 1 || details["field"] != "Idempotency-Key" {
		t.Fatalf("code=%q message=%q details=%v", code, message, details)
	}
}

func TestPersistedTextValidationRejectsOnlyNULWithFieldDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, field, message string
		validate             func() error
	}{
		{
			name: "title", field: "title", message: "Title contains an unsupported character.",
			validate: func() error {
				_, err := ValidateCreateInput(CreateInput{Title: "  Quarterly\x00 handoff  ", IdempotencyKey: "nul-title"})
				return err
			},
		},
		{
			name: "description", field: "description", message: "Description contains an unsupported character.",
			validate: func() error {
				_, err := ValidateCreateInput(CreateInput{Title: "Quarterly handoff", Description: "  Inspect\x00 loading dock  ", IdempotencyKey: "nul-description"})
				return err
			},
		},
		{
			name: "assignment reason", field: "reason", message: "Reason contains an unsupported character.",
			validate: func() error {
				_, err := ValidateAssignmentInput(AssignmentInput{AssigneeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Reason: "  Coverage\x00 changed  "})
				return err
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.validate()
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v", err)
			}
			code, message, details := ErrorContract(err)
			if code != "INVALID_INPUT" || message != test.message || details["field"] != test.field {
				t.Fatalf("code=%q message=%q details=%v", code, message, details)
			}
			if strings.Contains(strings.ToLower(message), "postgres") {
				t.Fatalf("message exposes persistence internals: %q", message)
			}
		})
	}
}

func TestPersistedTextValidationKeepsMaximumsAndOtherControls(t *testing.T) {
	t.Parallel()
	created, err := ValidateCreateInput(CreateInput{
		Title:          strings.Repeat("界", 200),
		Description:    strings.Repeat("界", 5000),
		IdempotencyKey: "persisted-text-boundary",
	})
	if err != nil || utf8.RuneCountInString(created.Title) != 200 || utf8.RuneCountInString(created.Description) != 5000 {
		t.Fatalf("create=%#v error=%v", created, err)
	}
	assignment, err := ValidateAssignmentInput(AssignmentInput{
		AssigneeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Reason:     strings.Repeat("界", 1000),
	})
	if err != nil || utf8.RuneCountInString(assignment.Reason) != 1000 {
		t.Fatalf("assignment=%#v error=%v", assignment, err)
	}
	created, err = ValidateCreateInput(CreateInput{
		Title:          "Quarterly\thandoff",
		Description:    "Inspect\u0085loading dock",
		IdempotencyKey: "persisted-text-controls",
	})
	if err != nil || created.Title != "Quarterly\thandoff" || created.Description != "Inspect\u0085loading dock" {
		t.Fatalf("create=%#v error=%v", created, err)
	}
	assignment, err = ValidateAssignmentInput(AssignmentInput{
		AssigneeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Reason:     "Coverage\tchanged",
	})
	if err != nil || assignment.Reason != "Coverage\tchanged" {
		t.Fatalf("assignment=%#v error=%v", assignment, err)
	}
}

func TestRoleAndLifecycleRules(t *testing.T) {
	t.Parallel()
	manager := auth.Principal{ID: "manager", Role: auth.RoleManager}
	worker := auth.Principal{ID: "worker", Role: auth.RoleWorker}
	other := auth.Principal{ID: "other", Role: auth.RoleWorker}

	for _, test := range []struct {
		name    string
		action  func() error
		wantErr error
	}{
		{name: "manager creates", action: func() error { return AuthorizeCreate(manager) }},
		{name: "worker cannot create", action: func() error { return AuthorizeCreate(worker) }, wantErr: ErrPermissionDenied},
		{name: "manager lists manager queue", action: func() error { return AuthorizeManager(manager) }},
		{name: "worker cannot list manager queue", action: func() error { return AuthorizeManager(worker) }, wantErr: ErrPermissionDenied},
		{name: "worker lists own queue", action: func() error { return AuthorizeWorker(worker) }},
		{name: "manager cannot list own queue", action: func() error { return AuthorizeWorker(manager) }, wantErr: ErrPermissionDenied},
		{name: "manager reads every item", action: func() error { return AuthorizeRead(manager, WorkItem{Status: StatusUnassigned}) }},
		{name: "assignee reads assigned item", action: func() error { return AuthorizeRead(worker, WorkItem{Status: StatusAssigned, AssigneeID: worker.ID}) }},
		{name: "other worker sees not found", action: func() error { return AuthorizeRead(other, WorkItem{Status: StatusAssigned, AssigneeID: worker.ID}) }, wantErr: ErrResourceNotFound},
		{name: "unassigned hidden from worker", action: func() error { return AuthorizeRead(worker, WorkItem{Status: StatusUnassigned}) }, wantErr: ErrResourceNotFound},
		{name: "positive version", action: func() error { return ValidateExpectedVersion(1) }},
		{name: "missing version", action: func() error { return ValidateExpectedVersion(0) }, wantErr: ErrExpectedVersionRequired},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.action()
			if test.wantErr == nil && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v want=%v", err, test.wantErr)
			}
		})
	}
}

func TestTransitionLifecycleMatrix(t *testing.T) {
	t.Parallel()
	manager := auth.Principal{ID: "manager", Role: auth.RoleManager}
	assignee := auth.Principal{ID: "worker", Role: auth.RoleWorker}
	other := auth.Principal{ID: "other", Role: auth.RoleWorker}
	tests := []struct {
		name      string
		actor     auth.Principal
		status    Status
		target    Status
		wantErr   error
		wantNoop  bool
		wantEvent EventType
	}{
		{name: "assigned to in progress", actor: assignee, status: StatusAssigned, target: StatusInProgress, wantEvent: EventMarkedInProgress},
		{name: "in progress to completed", actor: assignee, status: StatusInProgress, target: StatusCompleted, wantEvent: EventCompleted},
		{name: "repeat in progress", actor: assignee, status: StatusInProgress, target: StatusInProgress, wantNoop: true},
		{name: "repeat completed", actor: assignee, status: StatusCompleted, target: StatusCompleted, wantNoop: true},
		{name: "unassigned cannot start", actor: assignee, status: StatusUnassigned, target: StatusInProgress, wantErr: ErrResourceNotFound},
		{name: "assigned cannot complete directly", actor: assignee, status: StatusAssigned, target: StatusCompleted, wantErr: ErrInvalidTransition},
		{name: "in progress cannot return to assigned", actor: assignee, status: StatusInProgress, target: StatusAssigned, wantErr: ErrInvalidTransition},
		{name: "completed cannot start", actor: assignee, status: StatusCompleted, target: StatusInProgress, wantErr: ErrCompletedImmutable},
		{name: "manager cannot start", actor: manager, status: StatusAssigned, target: StatusInProgress, wantErr: ErrPermissionDenied},
		{name: "other worker cannot start", actor: other, status: StatusAssigned, target: StatusInProgress, wantErr: ErrResourceNotFound},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			item := WorkItem{Status: test.status}
			if test.status != StatusUnassigned {
				item.AssigneeID = assignee.ID
			}
			decision, err := ValidateTransition(test.actor, item, test.target)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error=%v want=%v", err, test.wantErr)
				}
				return
			}
			if err != nil || decision.Noop != test.wantNoop || decision.EventType != test.wantEvent {
				t.Fatalf("decision=%#v err=%v", decision, err)
			}
		})
	}
}

func TestAssignmentLifecycleMatrix(t *testing.T) {
	t.Parallel()
	manager := auth.Principal{ID: "manager", Role: auth.RoleManager}
	workerActor := auth.Principal{ID: "worker-actor", Role: auth.RoleWorker}
	alice := auth.User{ID: "alice", Role: auth.RoleWorker}
	bob := auth.User{ID: "bob", Role: auth.RoleWorker}
	managerUser := auth.User{ID: "manager-user", Role: auth.RoleManager}
	tests := []struct {
		name      string
		actor     auth.Principal
		item      WorkItem
		assignee  auth.User
		reason    string
		wantErr   error
		wantNoop  bool
		wantEvent EventType
	}{
		{name: "initial assignment", actor: manager, item: WorkItem{Status: StatusUnassigned}, assignee: alice, wantEvent: EventAssigned},
		{name: "same assignee noop", actor: manager, item: WorkItem{Status: StatusAssigned, AssigneeID: alice.ID}, assignee: alice, wantNoop: true},
		{name: "reasoned reassignment", actor: manager, item: WorkItem{Status: StatusAssigned, AssigneeID: alice.ID}, assignee: bob, reason: "coverage", wantEvent: EventReassigned},
		{name: "reason required", actor: manager, item: WorkItem{Status: StatusAssigned, AssigneeID: alice.ID}, assignee: bob, wantErr: ErrReassignmentReasonRequired},
		{name: "worker cannot assign", actor: workerActor, item: WorkItem{Status: StatusUnassigned}, assignee: alice, wantErr: ErrPermissionDenied},
		{name: "manager cannot be assignee", actor: manager, item: WorkItem{Status: StatusUnassigned}, assignee: managerUser, wantErr: ErrAssigneeMustBeWorker},
		{name: "in progress assignment conflict", actor: manager, item: WorkItem{Status: StatusInProgress, AssigneeID: alice.ID}, assignee: bob, reason: "coverage", wantErr: ErrAssignmentConflict},
		{name: "completed immutable", actor: manager, item: WorkItem{Status: StatusCompleted, AssigneeID: alice.ID}, assignee: bob, reason: "coverage", wantErr: ErrCompletedImmutable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			decision, err := ValidateAssignment(test.actor, test.item, test.assignee, test.reason)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error=%v want=%v", err, test.wantErr)
				}
				return
			}
			if err != nil || decision.Noop != test.wantNoop || decision.EventType != test.wantEvent {
				t.Fatalf("decision=%#v err=%v", decision, err)
			}
		})
	}
}
