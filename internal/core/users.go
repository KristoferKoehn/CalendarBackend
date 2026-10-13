package core

import (
	"context"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// UserService handles admin user management.
//
// Appointments is retained for interface stability but is no longer consulted:
// deleting a user deliberately leaves their appointments behind, so there is
// nothing to check before the delete.
type UserService struct {
	users        store.UserStore
	appointments store.AppointmentStore
	sessions     store.SessionStore
}

func NewUserService(users store.UserStore, appointments store.AppointmentStore, sessions store.SessionStore) *UserService {
	return &UserService{users: users, appointments: appointments, sessions: sessions}
}

// Directory returns every user, for any authenticated caller.
//
// Profiles are a shared directory: every signed-in user may see every other user,
// which is what makes a display name usable on a shared calendar. The role and
// agent flags are included, so this does reveal who the admins are — that is
// already visible through the UI's admin-only navigation.
func (s *UserService) Directory(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if actor.ID == uuid.Nil {
		return nil, apierror.Unauthenticated("Missing token")
	}
	return s.users.ListUsers(ctx)
}

// Get returns a single user's profile. Profiles are visible to every
// authenticated user, so there is no ownership or role check beyond being signed
// in.
func (s *UserService) Get(ctx context.Context, actor domain.User, id uuid.UUID) (domain.User, error) {
	if actor.ID == uuid.Nil {
		return domain.User{}, apierror.Unauthenticated("Missing token")
	}
	return s.users.GetUserByID(ctx, id)
}

// ValidateProfile checks a profile's rules without writing anything.
//
// Split out so registration can reject a bad profile before the account is created;
// otherwise a rejected profile would arrive after the fact, leaving somebody with an
// account they had been told they had not created.
func (s *UserService) ValidateProfile(in store.ProfileUpdate) error {
	return validateBirthday(in.BirthMonth, in.BirthDay)
}

// UpdateProfile applies the caller's own profile edits. Taking no target id is
// deliberate: it makes editing somebody else's profile structurally impossible,
// so no ownership check is needed and none can be bypassed.
func (s *UserService) UpdateProfile(ctx context.Context, actor domain.User, in store.ProfileUpdate) (domain.User, error) {
	if actor.ID == uuid.Nil {
		return domain.User{}, apierror.Unauthenticated("Missing token")
	}
	if err := s.ValidateProfile(in); err != nil {
		return domain.User{}, err
	}
	return s.users.UpdateUserProfile(ctx, actor.ID, in)
}

// validateBirthday keeps month and day consistent. They are optional, but when
// either is supplied both must be, and each must be in range. The database
// enforces the same rules; this exists to return a clean INVALID_INPUT rather
// than a raw constraint violation.
//
// A supplied-but-zero pair is rejected here rather than treated as a clear: an
// explicit `null` is how a caller clears a birthday.
func validateBirthday(month, day *int) error {
	if month == nil && day == nil {
		return nil
	}
	if month == nil || day == nil {
		return apierror.InvalidInput("birthMonth and birthDay must be provided together")
	}
	if *month < 1 || *month > 12 {
		return apierror.InvalidInput("birthMonth must be between 1 and 12")
	}
	if *day < 1 || *day > 31 {
		return apierror.InvalidInput("birthDay must be between 1 and 31")
	}
	return nil
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

// Delete removes a user as an administrative action. Admins may delete any
// account, including their own, subject to the guard rails below.
//
// A user's appointments are deliberately left in place: appointments carry the
// owner's email so they stay meaningful after the account is gone, and an admin
// removes unwanted ones with AppointmentService.Delete. Deleting the account
// therefore no longer requires deleting or blocking on its bookings.
func (s *UserService) Delete(ctx context.Context, actor domain.User, id uuid.UUID) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}

	if id == actor.ID {
		return domain.User{}, apierror.Conflict("Cannot delete your own account")
	}

	target, err := s.users.GetUserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
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

	return s.deleteUserRow(ctx, target)
}

// DeleteSelf lets an authenticated user remove their own account. It exists
// because Delete is admin-only: without this, a non-admin would have no way to
// delete themselves, and relaxing Delete's role check would let any user delete
// anybody.
//
// The last remaining admin cannot delete themselves either, so the system cannot
// be left with no administrator.
func (s *UserService) DeleteSelf(ctx context.Context, actor domain.User) (domain.User, error) {
	target, err := s.users.GetUserByID(ctx, actor.ID)
	if err != nil {
		return domain.User{}, err
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

	return s.deleteUserRow(ctx, target)
}

// deleteUserRow removes the user and their sessions. Appointments are left
// behind on purpose.
func (s *UserService) deleteUserRow(ctx context.Context, target domain.User) (domain.User, error) {
	if err := s.sessions.DeleteSessionsByUser(ctx, target.ID); err != nil {
		return domain.User{}, err
	}
	if err := s.users.DeleteUser(ctx, target.ID); err != nil {
		return domain.User{}, err
	}
	return target, nil
}
