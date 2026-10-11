package core

import (
	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

// Aliases and constructors keep the existing test call sites while sharing the
// in-memory implementations in internal/store/memory.
type fakeUserStore = memory.UserStore
type fakeResourceStore = memory.ResourceStore
type fakeAppointmentStore = memory.AppointmentStore
type fakeSessionStore = memory.SessionStore

func newFakeUserStore() *memory.UserStore               { return memory.NewUserStore() }
func newFakeResourceStore() *memory.ResourceStore       { return memory.NewResourceStore() }
func newFakeAppointmentStore() *memory.AppointmentStore { return memory.NewAppointmentStore() }
func newFakeSessionStore() *memory.SessionStore         { return memory.NewSessionStore() }

func testAdmin() domain.User {
	return domain.User{ID: uuid.New(), Email: "admin@example.com", Role: domain.RoleAdmin}
}

func testUser() domain.User {
	return domain.User{ID: uuid.New(), Email: "user@example.com", Role: domain.RoleUser}
}
