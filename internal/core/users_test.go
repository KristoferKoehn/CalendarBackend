package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// Profiles are a shared directory: any signed-in user may list everyone.
func TestUserDirectoryVisibleToAll(t *testing.T) {
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

	list, err := svc.Directory(context.Background(), testUser())
	if err != nil {
		t.Fatalf("a normal user should see the directory, got %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 users, got %d", len(list))
	}

	// An unauthenticated caller (zero user) is refused.
	if _, err := svc.Directory(context.Background(), domain.User{}); !apierror.IsCode(err, apierror.CodeUnauthenticated) {
		t.Fatalf("expected UNAUTHENTICATED, got %v", err)
	}
}

func TestUserUpdateProfile(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	me, err := authSvc.Register(context.Background(), "me@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if me.DisplayName != nil {
		t.Fatalf("a fresh account should have no display name, got %v", *me.DisplayName)
	}

	name := "Jane Doe"
	pronouns := "she/her"
	month, day := 3, 14
	updated, err := svc.UpdateProfile(context.Background(), me, store.ProfileUpdate{
		DisplayName: &name,
		Pronouns:    &pronouns,
		BirthMonth:  &month,
		BirthDay:    &day,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName == nil || *updated.DisplayName != name {
		t.Fatalf("expected display name %q, got %v", name, updated.DisplayName)
	}
	if updated.BirthMonth == nil || *updated.BirthMonth != 3 {
		t.Fatalf("expected birth month 3, got %v", updated.BirthMonth)
	}
	if updated.Label() != name {
		t.Fatalf("expected the label to be the name, got %q", updated.Label())
	}

	// A field that is not supplied is left alone.
	jobTitle := "Engineer"
	after, err := svc.UpdateProfile(context.Background(), me, store.ProfileUpdate{JobTitle: &jobTitle})
	if err != nil {
		t.Fatal(err)
	}
	if after.DisplayName == nil || *after.DisplayName != name {
		t.Fatalf("an omitted field was overwritten: %v", after.DisplayName)
	}
	if after.JobTitle == nil || *after.JobTitle != jobTitle {
		t.Fatalf("expected job title %q, got %v", jobTitle, after.JobTitle)
	}

	// An empty string clears the value.
	empty := ""
	cleared, err := svc.UpdateProfile(context.Background(), me, store.ProfileUpdate{DisplayName: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.DisplayName != nil {
		t.Fatalf("expected the display name to be cleared, got %v", *cleared.DisplayName)
	}
	if cleared.Label() != "me@example.com" {
		t.Fatalf("expected the label to fall back to the email, got %q", cleared.Label())
	}
}

func TestUserUpdateProfileBirthdayValidation(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	me, err := authSvc.Register(context.Background(), "me@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	monthOnly := 3
	if _, err := svc.UpdateProfile(context.Background(), me, store.ProfileUpdate{BirthMonth: &monthOnly}); !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT for a month without a day, got %v", err)
	}

	badMonth, goodDay := 13, 1
	if _, err := svc.UpdateProfile(context.Background(), me, store.ProfileUpdate{BirthMonth: &badMonth, BirthDay: &goodDay}); !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT for month 13, got %v", err)
	}

	goodMonth, badDay := 3, 32
	if _, err := svc.UpdateProfile(context.Background(), me, store.ProfileUpdate{BirthMonth: &goodMonth, BirthDay: &badDay}); !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT for day 32, got %v", err)
	}

	// An unauthenticated caller (zero user) is refused.
	if _, err := svc.UpdateProfile(context.Background(), domain.User{}, store.ProfileUpdate{}); !apierror.IsCode(err, apierror.CodeUnauthenticated) {
		t.Fatalf("expected UNAUTHENTICATED, got %v", err)
	}
}

func TestUserGetRequiresAuth(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	target, err := authSvc.Register(context.Background(), "target@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(context.Background(), testUser(), target.ID)
	if err != nil {
		t.Fatalf("a normal user should read a profile, got %v", err)
	}
	if got.Email != target.Email {
		t.Fatalf("expected %q, got %q", target.Email, got.Email)
	}

	if _, err := svc.Get(context.Background(), domain.User{}, target.ID); !apierror.IsCode(err, apierror.CodeUnauthenticated) {
		t.Fatalf("expected UNAUTHENTICATED, got %v", err)
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

// A user's appointments must survive deleting the account. The owner's email is
// denormalised onto the appointment so it stays meaningful, and an admin clears
// unwanted ones with AppointmentService.Delete.
func TestUserDeleteKeepsAppointments(t *testing.T) {
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
	apptID := uuid.New()
	if _, err := appointments.CreateAppointment(context.Background(), domain.Appointment{
		ID:         apptID,
		UserID:     target.ID,
		OwnerEmail: target.Email,
		ResourceID: uuid.New(),
		StartAt:    now,
		EndAt:      now.Add(time.Hour),
		Status:     domain.StatusBooked,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := svc.Delete(context.Background(), testAdmin(), target.ID)
	if err != nil {
		t.Fatalf("deleting a user with appointments should succeed, got %v", err)
	}
	if deleted.ID != target.ID {
		t.Fatalf("expected the deleted user back, got %+v", deleted)
	}

	kept, err := appointments.GetAppointmentByID(context.Background(), apptID)
	if err != nil {
		t.Fatalf("appointment should survive the owner's deletion, got %v", err)
	}
	if kept.OwnerEmail != "busy@example.com" {
		t.Fatalf("expected the owner email to be preserved, got %q", kept.OwnerEmail)
	}
	if kept.UserID != target.ID {
		t.Fatalf("expected user_id to remain as a historical reference, got %s", kept.UserID)
	}
}

func TestUserDeleteSelfService(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	me, err := authSvc.Register(context.Background(), "leaving@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := svc.DeleteSelf(context.Background(), me)
	if err != nil {
		t.Fatalf("a normal user should be able to delete themselves, got %v", err)
	}
	if deleted.ID != me.ID {
		t.Fatalf("expected the deleted user back, got %+v", deleted)
	}
	if _, err := users.GetUserByID(context.Background(), me.ID); !apierror.IsCode(err, apierror.CodeNotFound) {
		t.Fatalf("expected the account to be gone, got %v", err)
	}
}

func TestUserDeleteSelfLastAdminRejected(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	appointments := newFakeAppointmentStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users, appointments, sessions)

	u, err := authSvc.Register(context.Background(), "only-admin@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	role := domain.RoleAdmin
	admin, err := users.UpdateUser(context.Background(), u.ID, &role, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.DeleteSelf(context.Background(), admin); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT for the last admin, got %v", err)
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
