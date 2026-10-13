package core

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// DefaultResetTTL is how long a password reset link stays valid. Short on purpose:
// the link is a bearer credential for the account, and mailboxes are long-lived.
const DefaultResetTTL = 30 * time.Minute

// PasswordReset implements the forgotten-password flow.
//
// The shape of the flow is dictated by one rule: a request must never reveal whether
// an address has an account. So Request returns the same outcome for a known and an
// unknown address, and the caller turns that into an identical response.
type PasswordReset struct {
	users      store.UserStore
	tokens     store.AuthTokenStore
	sessions   store.SessionStore
	mailer     mail.Sender
	siteURL    string
	ttl        time.Duration
	now        func() time.Time
	minLatency time.Duration
}

// NewPasswordReset builds the service. mailer may be nil, in which case no mail is
// sent — the token is still created, which is what tests assert against.
func NewPasswordReset(
	users store.UserStore,
	tokens store.AuthTokenStore,
	sessions store.SessionStore,
	mailer mail.Sender,
	siteURL string,
	ttl time.Duration,
	now func() time.Time,
) *PasswordReset {
	if ttl <= 0 {
		ttl = DefaultResetTTL
	}
	return &PasswordReset{
		users:    users,
		tokens:   tokens,
		sessions: sessions,
		mailer:   mailer,
		siteURL:  siteURL,
		ttl:      ttl,
		now:      orNow(now),
		// A floor on how long Request takes, so the response time cannot be used to
		// tell a known address (which does work) from an unknown one (which does
		// not). Without this the difference is measurable.
		minLatency: 150 * time.Millisecond,
	}
}

// Request starts a reset. It reports whether a message was sent, which is for logs
// and tests only — callers must not put it in a response, or it becomes the
// account-existence oracle this method exists to avoid.
func (s *PasswordReset) Request(ctx context.Context, email string) (sent bool, err error) {
	started := s.now()

	u, _, lookupErr := s.users.GetUserByEmail(ctx, email)
	if lookupErr != nil {
		if apierror.IsCode(lookupErr, apierror.CodeNotFound) {
			// No such account. Report success and send nothing.
			s.padLatency(started)
			return false, nil
		}
		// A real failure is still surfaced; it says nothing about the account.
		return false, lookupErr
	}

	// Invalidate outstanding links before issuing a new one, so a reset requested
	// twice leaves only the newest usable.
	if err := s.tokens.DeleteUserAuthTokens(ctx, u.ID, purposePtr(domain.PurposePasswordReset)); err != nil {
		return false, err
	}

	raw, err := auth.NewToken()
	if err != nil {
		return false, apierror.Internal("failed to issue reset token")
	}

	token := domain.AuthToken{
		ID:        uuid.New(),
		UserID:    u.ID,
		Purpose:   domain.PurposePasswordReset,
		TokenHash: auth.HashToken(raw),
		ExpiresAt: s.now().Add(s.ttl),
		CreatedAt: s.now(),
	}
	if err := s.tokens.CreateAuthToken(ctx, token); err != nil {
		return false, err
	}

	if err := s.sendResetMail(ctx, u, raw); err != nil {
		// The token exists but the mail did not go out. Log the detail; the caller
		// still reports success so the response cannot be used to probe accounts.
		slog.Default().Error("password reset mail failed", "error", err, "userId", u.ID.String())
		s.padLatency(started)
		return false, nil
	}

	s.padLatency(started)
	// Without a mailer the token was created but nothing was delivered, and saying
	// otherwise would make the flag lie to logs and tests.
	return s.mailer != nil, nil
}

// Apply redeems a reset token and sets a new password.
//
// All the failure modes return one message. An attacker holding a wrong guess should
// not learn whether the token was unknown, already used, or expired.
func (s *PasswordReset) Apply(ctx context.Context, rawToken, newPassword string) error {
	invalid := apierror.InvalidInput("This reset link is invalid or has expired. Request a new one.")

	if rawToken == "" {
		return invalid
	}

	consumed, err := s.tokens.GetAuthTokenByHash(ctx, auth.HashToken(rawToken))
	if err != nil {
		if apierror.IsCode(err, apierror.CodeNotFound) {
			return invalid
		}
		return err
	}
	if consumed.Purpose != domain.PurposePasswordReset || !consumed.Usable(s.now()) {
		// Covers a verification token presented as a reset credential, a token that
		// was already used, and an expired one.
		return invalid
	}

	// The token is burned from here on, whether or not the rest succeeds. Otherwise a
	// failure part-way through would leave a still-live link that could be retried,
	// and it would also make the response distinguishable from the unknown-token case.
	tokenHash := auth.HashToken(rawToken)
	if _, err := s.tokens.ConsumeAuthToken(ctx, tokenHash, s.now()); err != nil {
		return invalid
	}
	succeeded := false
	defer func() {
		if !succeeded {
			// Do not leave a credential behind for an attempt that had no effect.
			_ = s.tokens.DeleteUserAuthTokens(ctx, consumed.UserID, purposePtr(domain.PurposePasswordReset))
		}
	}()
	userID := consumed.UserID

	// Resolve the owner from the token, then fetch the credential by email: that is
	// the only lookup the store exposes the hash on. The address comes from the
	// token's owner, so it is not attacker-controlled.
	owner, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		// The account was deleted between issuing and redeeming the link. Same opaque
		// answer as an unknown token.
		return invalid
	}
	owner, currentHash, err := s.users.GetUserByEmail(ctx, owner.Email)
	if err != nil {
		return invalid
	}

	// Refusing a repeat of the current password is a small usability guard, not a
	// security control: it costs nothing and avoids the confusing case where a reset
	// "succeeds" while changing nothing.
	if same, err := auth.VerifyPassword(newPassword, currentHash); err == nil && same {
		return apierror.InvalidInput("That is already your current password. Choose a different one.")
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return apierror.Internal("failed to hash password")
	}
	if err := s.users.UpdateUserPassword(ctx, owner.ID, hash); err != nil {
		return err
	}

	// Every existing session dies with the old password. A reset that leaves the
	// previous holder signed in has not actually recovered the account.
	if err := s.sessions.DeleteSessionsByUser(ctx, owner.ID); err != nil {
		return err
	}

	succeeded = true
	return nil
}

// padLatency waits out the remainder of minLatency, so that a lookup which found
// nothing and one that sent mail take about the same time.
func (s *PasswordReset) padLatency(started time.Time) {
	remaining := s.minLatency - s.now().Sub(started)
	if remaining <= 0 {
		return
	}
	time.Sleep(remaining)
}

func (s *PasswordReset) sendResetMail(ctx context.Context, u domain.User, rawToken string) error {
	if s.mailer == nil {
		return nil
	}

	link := s.siteURL + "/reset-password?token=" + rawToken
	minutes := int(s.ttl.Minutes())

	text := "Someone asked to reset the password for your CalendarSite account.\n\n" +
		"Choose a new password here:\n" + link + "\n\n" +
		"This link can only be used once, and it expires in " + plural(minutes, "minute") + ".\n\n" +
		"If you did not ask for this, you can ignore this message: your password has not\n" +
		"changed, and nobody else can use the link.\n"

	return s.mailer.Send(ctx, mail.Message{
		To:      u.Email,
		Subject: "Reset your CalendarSite password",
		Text:    text,
	})
}

// plural renders "1 minute" / "N minutes".
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

func purposePtr(p domain.TokenPurpose) *domain.TokenPurpose { return &p }
