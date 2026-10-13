package core

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// DefaultVerificationTTL is how long a verification link stays valid. Much longer than a
// password reset: the link only proves control of an address, so the worst a leaked one
// allows is confirming an address that could have been confirmed anyway, and real people
// click these hours or days later.
const DefaultVerificationTTL = 24 * time.Hour

// EmailVerification implements proving that a user controls their address.
//
// Verification is a nudge rather than a gate: an unverified account can still sign in and
// book. Gating it would lock out every account that existed before the feature, which is
// a support incident rather than a security win.
type EmailVerification struct {
	users   store.UserStore
	tokens  store.AuthTokenStore
	mailer  mail.Sender
	siteURL string
	ttl     time.Duration
	now     func() time.Time
}

// NewEmailVerification builds the service. A nil mailer creates tokens without sending,
// which is what the tests and an unconfigured local setup use.
func NewEmailVerification(
	users store.UserStore,
	tokens store.AuthTokenStore,
	mailer mail.Sender,
	siteURL string,
	ttl time.Duration,
	now func() time.Time,
) *EmailVerification {
	if ttl <= 0 {
		ttl = DefaultVerificationTTL
	}
	return &EmailVerification{
		users:   users,
		tokens:  tokens,
		mailer:  mailer,
		siteURL: siteURL,
		ttl:     ttl,
		now:     orNow(now),
	}
}

// IssueFor sends a verification link for a known user.
//
// Takes the user rather than an address so it can be called straight after a registration,
// where the account was just created and no lookup is needed.
//
// Any outstanding link is invalidated first, so asking again leaves only the newest one
// usable. Unlike a password reset this is not a secret being rotated, but two live links
// for one address is a thing to avoid rather than reason about.
func (s *EmailVerification) IssueFor(ctx context.Context, u domain.User) error {
	if u.EmailVerifiedAt != nil {
		return apierror.Conflict("That address is already verified.")
	}

	if err := s.tokens.DeleteUserAuthTokens(ctx, u.ID, purposePtr(domain.PurposeEmailVerification)); err != nil {
		return err
	}

	raw, err := auth.NewToken()
	if err != nil {
		return apierror.Internal("failed to issue verification token")
	}

	token := domain.AuthToken{
		ID:        uuid.New(),
		UserID:    u.ID,
		Purpose:   domain.PurposeEmailVerification,
		TokenHash: auth.HashToken(raw),
		ExpiresAt: s.now().Add(s.ttl),
		CreatedAt: s.now(),
	}
	if err := s.tokens.CreateAuthToken(ctx, token); err != nil {
		return err
	}

	if err := s.sendMail(ctx, u, raw); err != nil {
		// The token exists but the mail did not go out. Surface it so a caller can decide
		// whether that matters; the resend button is the recovery path either way.
		return err
	}
	return nil
}

// IssueForAddress sends a verification link to a known address.
//
// An unknown address is reported as success and sends nothing, because this is reachable
// from a signed-out endpoint and must not become a way to discover who has an account.
func (s *EmailVerification) IssueForAddress(ctx context.Context, email string) (bool, error) {
	u, _, err := s.users.GetUserByEmail(ctx, email)
	if err != nil {
		if apierror.IsCode(err, apierror.CodeNotFound) {
			return false, nil
		}
		return false, err
	}
	if u.EmailVerifiedAt != nil {
		// Already verified is not a failure to hide: the caller knows their own address.
		return false, apierror.Conflict("That address is already verified.")
	}
	if err := s.IssueFor(ctx, u); err != nil {
		return false, err
	}
	return true, nil
}

// Verify consumes a token and marks the address verified.
//
// All the ways a token can be unusable return one message, so a wrong guess learns
// nothing about whether it was unknown, already used, or expired.
func (s *EmailVerification) Verify(ctx context.Context, rawToken string) (domain.User, error) {
	invalid := apierror.InvalidInput(
		"This verification link is invalid or has expired. Request a new one.")

	if rawToken == "" {
		return domain.User{}, invalid
	}

	stored, err := s.tokens.GetAuthTokenByHash(ctx, auth.HashToken(rawToken))
	if err != nil {
		if apierror.IsCode(err, apierror.CodeNotFound) {
			return domain.User{}, invalid
		}
		return domain.User{}, err
	}
	if stored.Purpose != domain.PurposeEmailVerification || !stored.Usable(s.now()) {
		return domain.User{}, invalid
	}

	// Burned from here whether or not the rest succeeds, so a failed attempt cannot be
	// retried and the response does not differ from the unknown-token case.
	if _, err := s.tokens.ConsumeAuthToken(ctx, auth.HashToken(rawToken), s.now()); err != nil {
		return domain.User{}, invalid
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = s.tokens.DeleteUserAuthTokens(ctx, stored.UserID, purposePtr(domain.PurposeEmailVerification))
		}
	}()

	u, err := s.users.GetUserByID(ctx, stored.UserID)
	if err != nil {
		// Deleted between issuing and redeeming.
		return domain.User{}, invalid
	}

	// Idempotent at the store: verifying twice must not move the original timestamp.
	if err := s.users.MarkEmailVerified(ctx, u.ID, s.now()); err != nil {
		return domain.User{}, err
	}
	succeeded = true

	// Re-read so the caller gets the stored timestamp and the token is gone from the
	// user's list, rather than assembling a response by hand.
	updated, err := s.users.GetUserByID(ctx, u.ID)
	if err != nil {
		return u, nil
	}
	return updated, nil
}

func (s *EmailVerification) sendMail(ctx context.Context, u domain.User, rawToken string) error {
	if s.mailer == nil {
		return nil
	}

	link := s.siteURL + "/verify-email?token=" + rawToken
	hours := int(s.ttl.Hours())

	text := "Confirm this address to finish setting up your CalendarSite account.\n\n" +
		link + "\n\n" +
		"This link can only be used once, and it expires in " + plural(hours, "hour") + ".\n\n" +
		"If you did not create an account, you can ignore this message: nothing has\n" +
		"changed, and the address is not confirmed.\n"

	err := s.mailer.Send(ctx, mail.Message{
		To:      u.Email,
		Subject: "Confirm your CalendarSite address",
		Text:    text,
	})
	if err != nil {
		slog.Default().Error("verification mail failed", "error", err, "userId", u.ID.String())
		return err
	}
	return nil
}
