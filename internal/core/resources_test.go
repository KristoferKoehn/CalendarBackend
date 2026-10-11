package core

import (
	"context"
	"testing"

	"kristoferkoehn/CalendarBackend/internal/apierror"
)

func TestResourceCreateAdminOnly(t *testing.T) {
	resources := newFakeResourceStore()
	svc := NewResourceService(resources)

	if _, err := svc.Create(context.Background(), testUser(), CreateResourceInput{Name: "Room X"}); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	r, err := svc.Create(context.Background(), testAdmin(), CreateResourceInput{Name: "Room X"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Name != "Room X" || !r.Active || r.Timezone != "America/Los_Angeles" || r.Description != nil {
		t.Fatalf("unexpected resource: %+v", r)
	}
}

func TestResourceUpdateMerge(t *testing.T) {
	resources := newFakeResourceStore()
	svc := NewResourceService(resources)

	created, err := svc.Create(context.Background(), testAdmin(), CreateResourceInput{Name: "Room X"})
	if err != nil {
		t.Fatal(err)
	}

	desc := "updated"
	updated, err := svc.Update(context.Background(), testAdmin(), created.ID, UpdateResourceInput{Description: &desc})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Room X" || updated.Description == nil || *updated.Description != "updated" {
		t.Fatalf("unexpected update: %+v", updated)
	}
}

func TestResourceDeactivateAndList(t *testing.T) {
	resources := newFakeResourceStore()
	svc := NewResourceService(resources)

	a, err := svc.Create(context.Background(), testAdmin(), CreateResourceInput{Name: "Room A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(context.Background(), testAdmin(), CreateResourceInput{Name: "Room B"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Deactivate(context.Background(), testAdmin(), a.ID); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(context.Background(), testUser())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("expected only Room B, got %+v", list)
	}
}
