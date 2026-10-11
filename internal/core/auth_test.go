package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func TestAuthRegister(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	svc := NewAuthService(users, sessions, time.Hour, nil)

	u, err := svc.Register(context.Background(), "test@example.com", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.ID == uuid.Nil || u.Email != "test@example.com" || u.Role != domain.RoleUser {
		t.Fatalf("unexpected user: %+v", u)
	}

	if _, err := svc.Register(context.Background(), "test@example.com", "password123"); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestAuthLogin(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	svc := NewAuthService(users, sessions, time.Hour, nil)

	if _, err := svc.Register(context.Background(), "test@example.com", "password123"); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Login(context.Background(), "test@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.Token == "" || res.User.Email != "test@example.com" {
		t.Fatalf("unexpected login result: %+v", res)
	}
	if !res.ExpiresAt.After(time.Now()) {
		t.Fatal("expiresAt should be in the future")
	}

	if _, err := svc.Login(context.Background(), "test@example.com", "wrong"); !apierror.IsCode(err, apierror.CodeUnauthenticated) {
		t.Fatalf("expected UNAUTHENTICATED, got %v", err)
	}
	if _, err := svc.Login(context.Background(), "nobody@example.com", "password123"); !apierror.IsCode(err, apierror.CodeUnauthenticated) {
		t.Fatalf("expected UNAUTHENTICATED, got %v", err)
	}
}
