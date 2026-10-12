package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func newResource(t *testing.T, store *fakeResourceStore, name string, active bool) domain.Resource {
	t.Helper()
	r := domain.Resource{
		ID:        uuid.New(),
		Name:      name,
		Timezone:  "America/Los_Angeles",
		Active:    active,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := store.CreateResource(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAppointmentCreate(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room A", true)
	actor := testUser()

	a, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID:  room.ID,
		Title:       "Standup",
		Description: "Daily sync",
		Date:        "2026-10-10",
		StartTime:   "14:00",
		EndTime:     "15:00",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Status != domain.StatusBooked || a.UserID != actor.ID || a.ResourceID != room.ID {
		t.Fatalf("unexpected appointment: %+v", a)
	}
	if a.Title != "Standup" || a.Description != "Daily sync" {
		t.Fatalf("title/description not stored: %+v", a)
	}
	if got := a.StartAt.Format(time.RFC3339); got != "2026-10-10T21:00:00Z" {
		t.Fatalf("startAt = %s", got)
	}
	if a.StartAtLocal != "2026-10-10T14:00:00-07:00" {
		t.Fatalf("startAtLocal = %s", a.StartAtLocal)
	}
	if a.CancelledAt != nil {
		t.Fatal("cancelledAt should be nil")
	}
}

func TestAppointmentCreateInactiveResource(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Inactive", false)

	_, err := svc.Create(context.Background(), testUser(), CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "14:00", EndTime: "15:00",
	})
	if !apierror.IsCode(err, apierror.CodeNotFound) {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestAppointmentCreateDSTGap(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)

	_, err := svc.Create(context.Background(), testUser(), CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-03-08", StartTime: "02:30", EndTime: "03:30",
	})
	if !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestAppointmentCreateOverlap(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)
	actor := testUser()

	if _, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "14:00", EndTime: "15:00",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "14:30", EndTime: "15:30",
	})
	if !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestAppointmentListScoping(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)
	actorA := testUser()
	actorB := testUser()

	if _, err := svc.Create(context.Background(), actorA, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "10:00", EndTime: "11:00",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), actorB, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "11:00", EndTime: "12:00",
	}); err != nil {
		t.Fatal(err)
	}

	list, err := svc.List(context.Background(), actorA, AppointmentListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].UserID != actorA.ID {
		t.Fatalf("user should see only own appointments, got %+v", list)
	}

	all, err := svc.List(context.Background(), testAdmin(), AppointmentListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("admin should see all, got %d", len(all))
	}

	userID := actorB.ID
	filtered, err := svc.List(context.Background(), testAdmin(), AppointmentListInput{UserID: &userID})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].UserID != actorB.ID {
		t.Fatalf("admin userId filter failed: %+v", filtered)
	}
}

func TestAppointmentListDateRange(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)
	actor := testUser()

	if _, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "10:00", EndTime: "11:00",
	}); err != nil {
		t.Fatal(err)
	}

	from := "2026-10-10"
	to := "2026-10-10"
	list, err := svc.List(context.Background(), actor, AppointmentListInput{From: &from, To: &to})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 in range, got %d", len(list))
	}

	from = "2026-10-11"
	list, err = svc.List(context.Background(), actor, AppointmentListInput{From: &from})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 after range, got %d", len(list))
	}
}

func TestAppointmentListTitleFilter(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)
	actor := testUser()

	if _, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Title: "Standup", Date: "2026-10-10", StartTime: "10:00", EndTime: "11:00",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Title: "Planning", Date: "2026-10-10", StartTime: "11:00", EndTime: "12:00",
	}); err != nil {
		t.Fatal(err)
	}

	term := "stan"
	list, err := svc.List(context.Background(), actor, AppointmentListInput{Title: &term})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "Standup" {
		t.Fatalf("expected only Standup, got %+v", list)
	}
}

func TestAppointmentCancel(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)
	actor := testUser()
	a, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "10:00", EndTime: "11:00",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Cancel(context.Background(), testUser(), a.ID); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	cancelled, err := svc.Cancel(context.Background(), actor, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != domain.StatusCancelled || cancelled.CancelledAt == nil {
		t.Fatalf("unexpected cancel: %+v", cancelled)
	}

	if _, err := svc.Cancel(context.Background(), actor, a.ID); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestAppointmentReschedule(t *testing.T) {
	resources := newFakeResourceStore()
	appointments := newFakeAppointmentStore()
	svc := NewAppointmentService(appointments, resources, nil)

	room := newResource(t, resources, "Room", true)
	actor := testUser()
	a, err := svc.Create(context.Background(), actor, CreateAppointmentInput{
		ResourceID: room.ID, Title: "Standup", Description: "Daily sync", Date: "2026-10-10", StartTime: "10:00", EndTime: "11:00",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Self-overlap is allowed: move 10:00-11:00 to 10:30-11:30.
	res, err := svc.Reschedule(context.Background(), actor, a.ID, RescheduleAppointmentInput{
		Title: "Moved", Description: "Rescheduled", Date: "2026-10-10", StartTime: "10:30", EndTime: "11:30",
	})
	if err != nil {
		t.Fatalf("Reschedule self-overlap: %v", err)
	}
	if got := res.StartAt.Format(time.RFC3339); got != "2026-10-10T17:30:00Z" {
		t.Fatalf("rescheduled startAt = %s", got)
	}
	if res.Status != domain.StatusBooked || res.CancelledAt != nil {
		t.Fatalf("unexpected reschedule: %+v", res)
	}
	if res.Title != "Moved" || res.Description != "Rescheduled" {
		t.Fatalf("title/description not updated: %+v", res)
	}

	if _, err := svc.Reschedule(context.Background(), testUser(), a.ID, RescheduleAppointmentInput{
		Date: "2026-10-10", StartTime: "12:00", EndTime: "13:00",
	}); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	other := testUser()
	if _, err := svc.Create(context.Background(), other, CreateAppointmentInput{
		ResourceID: room.ID, Date: "2026-10-10", StartTime: "12:00", EndTime: "13:00",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reschedule(context.Background(), actor, a.ID, RescheduleAppointmentInput{
		Date: "2026-10-10", StartTime: "12:30", EndTime: "13:30",
	}); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}

	if _, err := svc.Cancel(context.Background(), actor, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reschedule(context.Background(), actor, a.ID, RescheduleAppointmentInput{
		Date: "2026-10-10", StartTime: "14:00", EndTime: "15:00",
	}); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT for cancelled, got %v", err)
	}
}
