package core

import (
	"context"
	"testing"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func TestUserListAdminOnly(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users)

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
	authSvc := NewAuthService(users, sessions, time.Hour, nil)
	svc := NewUserService(users)

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
