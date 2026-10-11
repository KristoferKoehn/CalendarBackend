//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/seed"
	"kristoferkoehn/CalendarBackend/internal/store"
	"kristoferkoehn/CalendarBackend/internal/store/postgres"
)

func TestUserStoreRoundTrip(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()

	u := domain.User{ID: uuid.New(), Email: "a@example.com", Role: domain.RoleUser, CreatedAt: time.Now().UTC()}
	if _, err := st.CreateUser(ctx, u, "hash"); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != u.Email {
		t.Fatalf("email = %s, want %s", got.Email, u.Email)
	}

	_, hash, err := st.GetUserByEmail(ctx, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "hash" {
		t.Fatalf("hash = %q, want %q", hash, "hash")
	}
}

func TestUserDuplicateEmailConflict(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()

	email := "dup@example.com"
	u := domain.User{ID: uuid.New(), Email: email, Role: domain.RoleUser, CreatedAt: time.Now().UTC()}
	if _, err := st.CreateUser(ctx, u, "hash"); err != nil {
		t.Fatal(err)
	}

	dup := domain.User{ID: uuid.New(), Email: email, Role: domain.RoleUser, CreatedAt: time.Now().UTC()}
	if _, err := st.CreateUser(ctx, dup, "hash"); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestExclusionConstraintOverlap(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()

	resource := insertResource(t, st)
	user := insertUser(t, st)

	base := time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)

	first := domain.Appointment{
		ID:         uuid.New(),
		UserID:     user.ID,
		ResourceID: resource.ID,
		StartAt:    base,
		EndAt:      base.Add(time.Hour),
		Status:     domain.StatusBooked,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if _, err := st.CreateAppointment(ctx, first); err != nil {
		t.Fatal(err)
	}

	overlap := domain.Appointment{
		ID:         uuid.New(),
		UserID:     user.ID,
		ResourceID: resource.ID,
		StartAt:    base.Add(30 * time.Minute),
		EndAt:      base.Add(90 * time.Minute),
		Status:     domain.StatusBooked,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if _, err := st.CreateAppointment(ctx, overlap); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT from exclusion constraint, got %v", err)
	}
}

func TestRescheduleSelfOverlap(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()

	resource := insertResource(t, st)
	user := insertUser(t, st)

	base := time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)
	a := domain.Appointment{
		ID:         uuid.New(),
		UserID:     user.ID,
		ResourceID: resource.ID,
		StartAt:    base,
		EndAt:      base.Add(time.Hour),
		Status:     domain.StatusBooked,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if _, err := st.CreateAppointment(ctx, a); err != nil {
		t.Fatal(err)
	}

	// Overlapping the old range must succeed because the row excludes itself.
	if _, err := st.RescheduleAppointment(ctx, store.RescheduleParams{
		ID:        a.ID,
		Start:     base.Add(30 * time.Minute),
		End:       base.Add(90 * time.Minute),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("self-overlap reschedule should succeed, got %v", err)
	}
}

func TestSeedIdempotent(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()

	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}

	resources, err := st.ListActiveResources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
}

func insertUser(t *testing.T, st *postgres.Store) domain.User {
	t.Helper()
	u := domain.User{ID: uuid.New(), Email: uuid.NewString() + "@example.com", Role: domain.RoleUser, CreatedAt: time.Now().UTC()}
	if _, err := st.CreateUser(context.Background(), u, "hash"); err != nil {
		t.Fatal(err)
	}
	return u
}

func insertResource(t *testing.T, st *postgres.Store) domain.Resource {
	t.Helper()
	r := domain.Resource{ID: uuid.New(), Name: "Room", Timezone: "America/Los_Angeles", Active: true, CreatedAt: time.Now().UTC()}
	if _, err := st.CreateResource(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}
