// Package seed inserts the seed data idempotently.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// Run inserts the two seed users and two seed resources if they do not exist.
func Run(ctx context.Context, users store.UserStore, resources store.ResourceStore) error {
	for _, spec := range userSpecs() {
		if _, _, err := users.GetUserByEmail(ctx, spec.email); err == nil {
			continue
		} else if !apierror.IsCode(err, apierror.CodeNotFound) {
			return fmt.Errorf("check user %s: %w", spec.email, err)
		}

		hash, err := auth.HashPassword("password123")
		if err != nil {
			return fmt.Errorf("hash password for %s: %w", spec.email, err)
		}

		u := domain.User{
			ID:        spec.id,
			Email:     spec.email,
			Role:      spec.role,
			IsAgent:   false,
			CreatedAt: time.Now().UTC(),
		}
		if _, err := users.CreateUser(ctx, u, hash); err != nil {
			return fmt.Errorf("create user %s: %w", spec.email, err)
		}
	}

	for _, spec := range resourceSpecs() {
		if _, err := resources.GetResourceByID(ctx, spec.id); err == nil {
			continue
		} else if !apierror.IsCode(err, apierror.CodeNotFound) {
			return fmt.Errorf("check resource %s: %w", spec.name, err)
		}

		r := domain.Resource{
			ID:          spec.id,
			Name:        spec.name,
			Description: spec.description,
			Timezone:    "America/Los_Angeles",
			Active:      true,
			CreatedAt:   time.Now().UTC(),
		}
		if _, err := resources.CreateResource(ctx, r); err != nil {
			return fmt.Errorf("create resource %s: %w", spec.name, err)
		}
	}

	return nil
}

type userSpec struct {
	id    uuid.UUID
	email string
	role  domain.Role
}

func userSpecs() []userSpec {
	return []userSpec{
		{id: seedID("user/admin@example.com"), email: "admin@example.com", role: domain.RoleAdmin},
		{id: seedID("user/user@example.com"), email: "user@example.com", role: domain.RoleUser},
	}
}

type resourceSpec struct {
	id          uuid.UUID
	name        string
	description *string
}

func resourceSpecs() []resourceSpec {
	return []resourceSpec{
		{id: seedID("resource/Room A"), name: "Room A", description: strPtr("Main conference room")},
		{id: seedID("resource/Room B"), name: "Room B", description: strPtr("Small meeting room")},
	}
}

func seedID(name string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
}

func strPtr(s string) *string {
	return &s
}
