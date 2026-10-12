package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func TestUserListAdminOnly(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	if _, err := authSvc.Register(context.Background(), "a@example.com", "password123"); err != nil {
		t.Fatal(err)
	}
	if _, err := authSvc.Register(context.Background(), "b@example.com", "password123"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.List(context.Background(), testUser()); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	list, err := svc.List(context.Background(), testAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 users, got %d", len(list))
	}
}

func TestUserUpdate(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	u, err := authSvc.Register(context.Background(), "a@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Update(context.Background(), testAdmin(), u.ID, UpdateUserInput{}); !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}

	role := domain.RoleAdmin
	isAgent := true
	updated, err := svc.Update(context.Background(), testAdmin(), u.ID, UpdateUserInput{Role: &role, IsAgent: &isAgent})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != domain.RoleAdmin || !updated.IsAgent {
		t.Fatalf("unexpected update: %+v", updated)
	}
}

func TestUserDelete(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	target, err := authSvc.Register(context.Background(), "delete-me@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := svc.Delete(context.Background(), testAdmin(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Email != target.Email {
		t.Fatalf("expected email %q, got %q", target.Email, deleted.Email)
	}
	if _, err := users.GetUserByID(context.Background(), target.ID); !apierror.IsCode(err, apierror.CodeNotFound) {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestUserDeleteForbidden(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	target, err := authSvc.Register(context.Background(), "delete-me@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Delete(context.Background(), testUser(), target.ID); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestUserDeleteSelf(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	u, err := authSvc.Register(context.Background(), "admin@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	role := domain.RoleAdmin
	admin, err := users.UpdateUser(context.Background(), u.ID, &role, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Delete(context.Background(), admin, admin.ID); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestUserDeleteLastAdmin(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	u, err := authSvc.Register(context.Background(), "admin@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	role := domain.RoleAdmin
	admin, err := users.UpdateUser(context.Background(), u.ID, &role, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Delete(context.Background(), testAdmin(), admin.ID); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestUserDeleteWithAppointments(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	target, err := authSvc.Register(context.Background(), "busy@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := appointments.CreateAppointment(context.Background(), domain.Appointment{
		ID:         uuid.New(),
		UserID:     target.ID,
		ResourceID: uuid.New(),
		StartAt:    now,
		EndAt:      now.Add(time.Hour),
		Status:     domain.StatusBooked,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Delete(context.Background(), testAdmin(), target.ID); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestUserDeleteNotFound(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	svc := NewUserService(users, appointments, sessions)

	if _, err := svc.Delete(context.Background(), testAdmin(), uuid.New()); !apierror.IsCode(err, apierror.CodeNotFound) {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}
