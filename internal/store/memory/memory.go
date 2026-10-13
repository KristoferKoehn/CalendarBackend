// Package memory provides in-memory implementations of the store interfaces
// for tests and local development.
package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
	"kristoferkoehn/CalendarBackend/internal/timeutil"
)

var (
	_ store.UserStore        = (*UserStore)(nil)
	_ store.ResourceStore    = (*ResourceStore)(nil)
	_ store.AppointmentStore = (*AppointmentStore)(nil)
	_ store.SessionStore     = (*SessionStore)(nil)
	_ store.AuthTokenStore   = (*AuthTokenStore)(nil)
)

// UserStore is an in-memory store.UserStore.
type UserStore struct {
	byID    map[uuid.UUID]userRecord
	byEmail map[string]uuid.UUID
}

type userRecord struct {
	user domain.User
	hash string
}

func NewUserStore() *UserStore {
	return &UserStore{
		byID:    make(map[uuid.UUID]userRecord),
		byEmail: make(map[string]uuid.UUID),
	}
}

func (s *UserStore) CreateUser(_ context.Context, u domain.User, hash string) (domain.User, error) {
	if _, ok := s.byEmail[u.Email]; ok {
		return domain.User{}, apierror.Conflict("Email already in use")
	}
	s.byID[u.ID] = userRecord{user: u, hash: hash}
	s.byEmail[u.Email] = u.ID
	return u, nil
}

func (s *UserStore) GetUserByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	rec, ok := s.byID[id]
	if !ok {
		return domain.User{}, apierror.NotFound("User not found")
	}
	return rec.user, nil
}

func (s *UserStore) GetUserByEmail(_ context.Context, email string) (domain.User, string, error) {
	id, ok := s.byEmail[email]
	if !ok {
		return domain.User{}, "", apierror.NotFound("User not found")
	}
	rec := s.byID[id]
	return rec.user, rec.hash, nil
}

func (s *UserStore) ListUsers(_ context.Context) ([]domain.User, error) {
	out := make([]domain.User, 0, len(s.byID))
	for _, rec := range s.byID {
		out = append(out, rec.user)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (s *UserStore) DeleteUser(_ context.Context, id uuid.UUID) error {
	rec, ok := s.byID[id]
	if !ok {
		return apierror.NotFound("User not found")
	}
	delete(s.byID, id)
	delete(s.byEmail, rec.user.Email)
	return nil
}

func (s *UserStore) UpdateUser(_ context.Context, id uuid.UUID, role *domain.Role, isAgent *bool) (domain.User, error) {
	rec, ok := s.byID[id]
	if !ok {
		return domain.User{}, apierror.NotFound("User not found")
	}
	if role != nil {
		rec.user.Role = *role
	}
	if isAgent != nil {
		rec.user.IsAgent = *isAgent
	}
	s.byID[id] = rec
	return rec.user, nil
}

// UpdateUserProfile mirrors the postgres adapter: a nil field is left alone, and a
// pointer to an empty value clears it.
func (s *UserStore) UpdateUserProfile(_ context.Context, id uuid.UUID, p store.ProfileUpdate) (domain.User, error) {
	rec, ok := s.byID[id]
	if !ok {
		return domain.User{}, apierror.NotFound("User not found")
	}
	if p.DisplayName != nil {
		rec.user.DisplayName = emptyToNil(*p.DisplayName)
	}
	if p.BirthMonth != nil {
		rec.user.BirthMonth = p.BirthMonth
	}
	if p.BirthDay != nil {
		rec.user.BirthDay = p.BirthDay
	}
	if p.Pronouns != nil {
		rec.user.Pronouns = emptyToNil(*p.Pronouns)
	}
	if p.JobTitle != nil {
		rec.user.JobTitle = emptyToNil(*p.JobTitle)
	}
	if p.Bio != nil {
		rec.user.Bio = emptyToNil(*p.Bio)
	}
	s.byID[id] = rec
	return rec.user, nil
}

// emptyToNil matches the application's "an empty string means unset" rule, so the
// in-memory store behaves like the real one from a reader's point of view.
func emptyToNil(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// UpdateUserPassword replaces the stored hash, mirroring the postgres adapter.
func (s *UserStore) UpdateUserPassword(_ context.Context, id uuid.UUID, hash string) error {
	rec, ok := s.byID[id]
	if !ok {
		return apierror.NotFound("User not found")
	}
	rec.hash = hash
	s.byID[id] = rec
	return nil
}

// MarkEmailVerified is idempotent, matching the postgres adapter: verifying twice
// must not move the original timestamp.
func (s *UserStore) MarkEmailVerified(_ context.Context, id uuid.UUID, at time.Time) error {
	rec, ok := s.byID[id]
	if !ok {
		return apierror.NotFound("User not found")
	}
	if rec.user.EmailVerifiedAt == nil {
		when := at
		rec.user.EmailVerifiedAt = &when
		s.byID[id] = rec
	}
	return nil
}

// AuthTokenStore is an in-memory store.AuthTokenStore.
type AuthTokenStore struct {
	byHash map[string]domain.AuthToken
}

func NewAuthTokenStore() *AuthTokenStore {
	return &AuthTokenStore{byHash: make(map[string]domain.AuthToken)}
}

func (s *AuthTokenStore) CreateAuthToken(_ context.Context, t domain.AuthToken) error {
	if _, ok := s.byHash[t.TokenHash]; ok {
		return apierror.Conflict("Token already exists")
	}
	s.byHash[t.TokenHash] = t
	return nil
}

func (s *AuthTokenStore) GetAuthTokenByHash(_ context.Context, hash string) (domain.AuthToken, error) {
	t, ok := s.byHash[hash]
	if !ok {
		return domain.AuthToken{}, apierror.NotFound("token not found")
	}
	return t, nil
}

// ConsumeAuthToken mirrors the postgres adapter's single-statement guarantee: the
// liveness check and the write happen together, so a second redemption of the same
// token cannot succeed.
func (s *AuthTokenStore) ConsumeAuthToken(_ context.Context, hash string, now time.Time) (domain.AuthToken, error) {
	t, ok := s.byHash[hash]
	if !ok {
		return domain.AuthToken{}, apierror.NotFound("token is not valid")
	}
	if !t.Usable(now) {
		return domain.AuthToken{}, apierror.NotFound("token is not valid")
	}
	consumed := now
	t.ConsumedAt = &consumed
	s.byHash[hash] = t
	return t, nil
}

func (s *AuthTokenStore) DeleteUserAuthTokens(_ context.Context, userID uuid.UUID, purpose *domain.TokenPurpose) error {
	for hash, t := range s.byHash {
		if t.UserID != userID {
			continue
		}
		if purpose != nil && t.Purpose != *purpose {
			continue
		}
		delete(s.byHash, hash)
	}
	return nil
}

// UserTokenCount reports how many tokens a user currently has. It exists so tests
// can assert that a token was issued or cleared without knowing its raw value.
func (s *AuthTokenStore) UserTokenCount(userID uuid.UUID) int {
	n := 0
	for _, t := range s.byHash {
		if t.UserID == userID {
			n++
		}
	}
	return n
}

// ResourceStore is an in-memory store.ResourceStore.
type ResourceStore struct {
	byID  map[uuid.UUID]domain.Resource
	order []uuid.UUID
}

func NewResourceStore() *ResourceStore {
	return &ResourceStore{byID: make(map[uuid.UUID]domain.Resource)}
}

func (s *ResourceStore) CreateResource(_ context.Context, r domain.Resource) (domain.Resource, error) {
	s.byID[r.ID] = r
	s.order = append(s.order, r.ID)
	return r, nil
}

func (s *ResourceStore) GetResourceByID(_ context.Context, id uuid.UUID) (domain.Resource, error) {
	r, ok := s.byID[id]
	if !ok {
		return domain.Resource{}, apierror.NotFound("Resource not found")
	}
	return r, nil
}

func (s *ResourceStore) ListActiveResources(_ context.Context) ([]domain.Resource, error) {
	out := make([]domain.Resource, 0)
	for _, id := range s.order {
		if r := s.byID[id]; r.Active {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *ResourceStore) UpdateResource(_ context.Context, r domain.Resource) (domain.Resource, error) {
	if _, ok := s.byID[r.ID]; !ok {
		return domain.Resource{}, apierror.NotFound("Resource not found")
	}
	s.byID[r.ID] = r
	return r, nil
}

func (s *ResourceStore) DeactivateResource(_ context.Context, id uuid.UUID) (domain.Resource, error) {
	r, ok := s.byID[id]
	if !ok {
		return domain.Resource{}, apierror.NotFound("Resource not found")
	}
	r.Active = false
	s.byID[id] = r
	return r, nil
}

// AppointmentStore is an in-memory store.AppointmentStore.
type AppointmentStore struct {
	byID  map[uuid.UUID]domain.Appointment
	order []uuid.UUID
}

func NewAppointmentStore() *AppointmentStore {
	return &AppointmentStore{byID: make(map[uuid.UUID]domain.Appointment)}
}

func (s *AppointmentStore) CreateAppointment(_ context.Context, a domain.Appointment) (domain.Appointment, error) {
	s.byID[a.ID] = a
	s.order = append(s.order, a.ID)
	return a, nil
}

func (s *AppointmentStore) GetAppointmentByID(_ context.Context, id uuid.UUID) (domain.Appointment, error) {
	a, ok := s.byID[id]
	if !ok {
		return domain.Appointment{}, apierror.NotFound("Appointment not found")
	}
	return a, nil
}

func (s *AppointmentStore) DeleteAppointment(_ context.Context, id uuid.UUID) error {
	if _, ok := s.byID[id]; !ok {
		return apierror.NotFound("Appointment not found")
	}
	delete(s.byID, id)
	for i, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}

func (s *AppointmentStore) ListAppointments(_ context.Context, f store.AppointmentFilter) ([]domain.Appointment, error) {
	out := make([]domain.Appointment, 0)
	for _, id := range s.order {
		a := s.byID[id]
		if f.ResourceID != nil && a.ResourceID != *f.ResourceID {
			continue
		}
		if f.UserID != nil && a.UserID != *f.UserID {
			continue
		}
		if f.Title != nil && !strings.Contains(strings.ToLower(a.Title), strings.ToLower(*f.Title)) {
			continue
		}
		if f.Status != nil && a.Status != *f.Status {
			continue
		}
		if f.From != nil && a.StartAt.Before(*f.From) {
			continue
		}
		if f.To != nil && !a.StartAt.Before(*f.To) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *AppointmentStore) CancelAppointment(_ context.Context, id uuid.UUID, cancelledAt time.Time) (domain.Appointment, error) {
	a, ok := s.byID[id]
	if !ok {
		return domain.Appointment{}, apierror.NotFound("Appointment not found")
	}
	a.Status = domain.StatusCancelled
	a.CancelledAt = &cancelledAt
	a.UpdatedAt = cancelledAt
	s.byID[id] = a
	return a, nil
}

func (s *AppointmentStore) RescheduleAppointment(_ context.Context, p store.RescheduleParams) (domain.Appointment, error) {
	a, ok := s.byID[p.ID]
	if !ok {
		return domain.Appointment{}, apierror.NotFound("Appointment not found")
	}
	a.Title = p.Title
	a.Description = p.Description
	a.StartAt = p.Start
	a.EndAt = p.End
	a.StartAtLocal = timeutil.FormatLocal(p.Start)
	a.EndAtLocal = timeutil.FormatLocal(p.End)
	a.UpdatedAt = p.UpdatedAt
	a.Status = domain.StatusBooked
	a.CancelledAt = nil
	s.byID[p.ID] = a
	return a, nil
}

func (s *AppointmentStore) HasOverlap(_ context.Context, resourceID uuid.UUID, start, end time.Time, excludeID uuid.UUID) (bool, error) {
	for _, id := range s.order {
		a := s.byID[id]
		if a.ID == excludeID {
			continue
		}
		if a.ResourceID != resourceID || a.Status != domain.StatusBooked {
			continue
		}
		if a.StartAt.Before(end) && a.EndAt.After(start) {
			return true, nil
		}
	}
	return false, nil
}

// SessionStore is an in-memory store.SessionStore.
type SessionStore struct {
	byHash map[string]domain.Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{byHash: make(map[string]domain.Session)}
}

func (s *SessionStore) CreateSession(_ context.Context, sess domain.Session) error {
	s.byHash[sess.TokenHash] = sess
	return nil
}

func (s *SessionStore) DeleteSessionsByUser(_ context.Context, userID uuid.UUID) error {
	for hash, sess := range s.byHash {
		if sess.UserID == userID {
			delete(s.byHash, hash)
		}
	}
	return nil
}

func (s *SessionStore) GetSessionByTokenHash(_ context.Context, hash string) (domain.Session, error) {
	sess, ok := s.byHash[hash]
	if !ok {
		return domain.Session{}, apierror.NotFound("Session not found")
	}
	return sess, nil
}
