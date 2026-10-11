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
	users store.UserStore
}

func NewUserService(users store.UserStore) *UserService {
	return &UserService{users: users}
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
