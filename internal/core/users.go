package core

import (
	"context"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// UserService handles admin user management.
type UserService struct {
	users        store.UserStore
	appointments store.AppointmentStore
	sessions     store.SessionStore
}

func NewUserService(users store.UserStore, appointments store.AppointmentStore, sessions store.SessionStore) *UserService {
	return &UserService{users: users, appointments: appointments, sessions: sessions}
}

func (s *UserService) List(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.users.ListUsers(ctx)
}

func (s *UserService) Update(ctx context.Context, actor domain.User, id uuid.UUID, in UpdateUserInput) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}
	if in.Role == nil && in.IsAgent == nil {
		return domain.User{}, apierror.InvalidInput("at least one of role or isAgent is required")
	}
	return s.users.UpdateUser(ctx, id, in.Role, in.IsAgent)
}

func (s *UserService) Delete(ctx context.Context, actor domain.User, id uuid.UUID) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}

	target, err := s.users.GetUserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if target.ID == actor.ID {
		return domain.User{}, apierror.Conflict("Cannot delete your own account")
	}

	if target.Role == domain.RoleAdmin {
		all, err := s.users.ListUsers(ctx)
		if err != nil {
			return domain.User{}, err
		}
		adminCount := 0
		for _, u := range all {
			if u.Role == domain.RoleAdmin {
				adminCount++
			}
		}
		if adminCount <= 1 {
			return domain.User{}, apierror.Conflict("Cannot delete the last admin")
		}
	}

	appts, err := s.appointments.ListAppointments(ctx, store.AppointmentFilter{UserID: &id})
	if err != nil {
		return domain.User{}, err
	}
	if len(appts) > 0 {
		return domain.User{}, apierror.Conflict("User has appointments")
	}

	if err := s.sessions.DeleteSessionsByUser(ctx, id); err != nil {
		return domain.User{}, err
	}
	if err := s.users.DeleteUser(ctx, id); err != nil {
		return domain.User{}, err
	}
	return target, nil
}
