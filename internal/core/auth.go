package core

import (
	"context"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// AuthService handles registration and login.
type AuthService struct {
	users    store.UserStore
	sessions store.SessionStore
	ttl      time.Duration
	now      func() time.Time
}

func NewAuthService(users store.UserStore, sessions store.SessionStore, ttl time.Duration, now func() time.Time) *AuthService {
	return &AuthService{
		users:    users,
		sessions: sessions,
		ttl:      ttl,
		now:      orNow(now),
	}
}

// Register creates a user and returns it. The caller normalizes email and
// validates the password before calling.
func (s *AuthService) Register(ctx context.Context, email, password string) (domain.User, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return domain.User{}, apierror.Internal("failed to hash password")
	}

	u := domain.User{
		ID:        uuid.New(),
		Email:     email,
		Role:      domain.RoleUser,
		IsAgent:   false,
		CreatedAt: s.now(),
	}
	return s.users.CreateUser(ctx, u, hash)
}

// Login verifies the password and issues a bearer token.
func (s *AuthService) Login(ctx context.Context, email, password string) (LoginResult, error) {
	u, hash, err := s.users.GetUserByEmail(ctx, email)
	if err != nil {
		if apierror.IsCode(err, apierror.CodeNotFound) {
			return LoginResult{}, apierror.Unauthenticated("Invalid email or password")
		}
		return LoginResult{}, err
	}

	ok, err := auth.VerifyPassword(password, hash)
	if err != nil {
		return LoginResult{}, apierror.Internal("failed to verify password")
	}
	if !ok {
		return LoginResult{}, apierror.Unauthenticated("Invalid email or password")
	}

	// Deliberately no emailVerifiedAt check. Verification gates reaching the site, not the
	// ability to sign in: somebody who has not confirmed their address can still get in and
	// see the prompt that tells them to, which is far easier to act on than a login that
	// refuses without saying why.
	return s.IssueSession(ctx, u)
}

// IssueSession creates a signed-in session for a user who has already been proven.
//
// Split out of Login so redeeming a verification link can sign somebody in through exactly
// the same path. That link proves control of the address, so it is at least as strong a
// credential as a password, and having one place that mints a session means the token
// length, hashing and expiry cannot drift between the two.
func (s *AuthService) IssueSession(ctx context.Context, u domain.User) (LoginResult, error) {
	token, err := auth.NewToken()
	if err != nil {
		return LoginResult{}, apierror.Internal("failed to issue token")
	}

	expiresAt := s.now().Add(s.ttl)
	sess := domain.Session{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: auth.HashToken(token),
		ExpiresAt: expiresAt,
	}
	if err := s.sessions.CreateSession(ctx, sess); err != nil {
		return LoginResult{}, err
	}

	return LoginResult{Token: token, ExpiresAt: expiresAt, User: u}, nil
}
