package workitems

import (
	"errors"
	"testing"

	"github.com/eidrogo/work-management-api/internal/auth"
)

func TestRoleAndLifecycleRules(t *testing.T) {
	t.Parallel()
	manager := auth.Principal{ID: "m", Role: auth.RoleManager}
	worker := auth.Principal{ID: "w", Role: auth.RoleWorker}
	other := auth.Principal{ID: "o", Role: auth.RoleWorker}
	if err := AuthorizeCreate(manager); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(AuthorizeCreate(worker), ErrPermissionDenied) {
		t.Fatal("worker create should be denied")
	}
	item := WorkItem{Status: StatusAssigned, AssigneeID: worker.ID}
	if _, err := ValidateTransition(worker, item, StatusInProgress); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(AuthorizeRead(other, item), ErrResourceNotFound) {
		t.Fatal("other worker should not read item")
	}
	if !errors.Is(ValidateExpectedVersion(0), ErrExpectedVersionRequired) {
		t.Fatal("version should be required")
	}
}
