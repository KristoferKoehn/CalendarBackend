package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

type verifyEnv struct {
	svc    *EmailVerification
	users  *memory.UserStore
	tokens *memory.AuthTokenStore
	mailer *mail.Stub
	user   domain.User
	now    time.Time
}

func newVerifyEnv(t *testing.T) *verifyEnv {
	t.Helper()

	users := memory.NewUserStore()
	tokens := memory.NewAuthTokenStore()
	mailer := mail.NewStub()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	u, err := users.CreateUser(context.Background(), domain.User{
		ID:        uuid.New(),
		Email:     "booker@example.com",
		Role:      domain.RoleUser,
		CreatedAt: now,
	}, hash)
	if err != nil {
		t.Fatal(err)
	}

	env := &verifyEnv{users: users, tokens: tokens, mailer: mailer, user: u, now: now}
	// The clock reads env.now through the pointer, so a test can advance time.
	env.svc = NewEmailVerification(users, tokens, mailer, "https://example.test",
		DefaultVerificationTTL, func() time.Time { return env.now })
	return env
}

/** The raw token from the link in the recorded message. */
func tokenFromVerificationMail(t *testing.T, m mail.Message) string {
	t.Helper()
	const marker = "/verify-email?token="
	i := strings.Index(m.Text, marker)
	if i < 0 {
		t.Fatalf("no verification link in the body:\n%s", m.Text)
	}
	rest := m.Text[i+len(marker):]
	if end := strings.IndexAny(rest, "\r\n "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestIssueForSendsALinkThatVerifies(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	if err := env.svc.IssueFor(ctx, env.user); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg, ok := env.mailer.Last()
	if !ok {
		t.Fatal("expected a message")
	}
	if msg.To != "booker@example.com" {
		t.Fatalf("unexpected recipient %q", msg.To)
	}
	if !strings.Contains(msg.Text, "https://example.test/verify-email?token=") {
		t.Fatalf("link does not use the public site origin:\n%s", msg.Text)
	}
	// The body has to tell the reader what to do if they did not sign up.
	if !strings.Contains(msg.Text, "only be used once") ||
		!strings.Contains(msg.Text, "you can ignore") {
		t.Fatalf("body omits the single-use or ignore-it reassurance:\n%s", msg.Text)
	}

	updated, err := env.svc.Verify(ctx, tokenFromVerificationMail(t, msg))
	if err != nil {
		t.Fatalf("expected verification to succeed: %v", err)
	}
	if updated.EmailVerifiedAt == nil {
		t.Fatal("the returned user is not marked verified")
	}

	// And it is persisted, not just returned.
	stored, err := env.users.GetUserByID(ctx, env.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.EmailVerifiedAt == nil {
		t.Fatal("the stored user is not marked verified")
	}
}

func TestVerifyIsSingleUse(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	if err := env.svc.IssueFor(ctx, env.user); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()
	token := tokenFromVerificationMail(t, msg)

	if _, err := env.svc.Verify(ctx, token); err != nil {
		t.Fatalf("the first use should succeed: %v", err)
	}
	if _, err := env.svc.Verify(ctx, token); err == nil {
		t.Fatal("the same token worked twice")
	}
}

func TestVerifyRefusesUnusableTokens(t *testing.T) {
	ctx := context.Background()

	t.Run("garbage and empty", func(t *testing.T) {
		env := newVerifyEnv(t)
		for _, token := range []string{"", "not-a-token", strings.Repeat("a", 64)} {
			if _, err := env.svc.Verify(ctx, token); err == nil {
				t.Fatalf("token %q was accepted", token)
			}
		}
	})

	t.Run("expired", func(t *testing.T) {
		env := newVerifyEnv(t)
		if err := env.svc.IssueFor(ctx, env.user); err != nil {
			t.Fatal(err)
		}
		msg, _ := env.mailer.Last()
		token := tokenFromVerificationMail(t, msg)

		env.now = env.now.Add(DefaultVerificationTTL + time.Second)
		if _, err := env.svc.Verify(ctx, token); err == nil {
			t.Fatal("an expired token was accepted")
		}
	})

	t.Run("a reset token is not a verification token", func(t *testing.T) {
		env := newVerifyEnv(t)
		raw := "reset-token-raw-value"
		if err := env.tokens.CreateAuthToken(ctx, domain.AuthToken{
			ID: uuid.New(), UserID: env.user.ID,
			Purpose:   domain.PurposePasswordReset,
			TokenHash: auth.HashToken(raw),
			ExpiresAt: env.now.Add(time.Hour), CreatedAt: env.now,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := env.svc.Verify(ctx, raw); err == nil {
			t.Fatal("a reset token was accepted as a verification credential")
		}
	})
}

func TestVerifyAllFailureModesLookTheSame(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	// An expired token should be indistinguishable from an unknown one, so a wrong guess
	// learns nothing about whether it was real.
	if err := env.svc.IssueFor(ctx, env.user); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()
	expired := tokenFromVerificationMail(t, msg)
	env.now = env.now.Add(DefaultVerificationTTL + time.Second)

	_, expiredErr := env.svc.Verify(ctx, expired)
	_, unknownErr := env.svc.Verify(ctx, "definitely-not-a-token")

	if expiredErr == nil || unknownErr == nil {
		t.Fatal("expected both to fail")
	}
	if expiredErr.Error() != unknownErr.Error() {
		t.Fatalf("messages differ:\n  expired: %q\n  unknown: %q", expiredErr.Error(), unknownErr.Error())
	}
}

func TestIssueForRefusesAnAlreadyVerifiedAddress(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	if err := env.users.MarkEmailVerified(ctx, env.user.ID, env.now); err != nil {
		t.Fatal(err)
	}
	verified, err := env.users.GetUserByID(ctx, env.user.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = env.svc.IssueFor(ctx, verified)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
	if len(env.mailer.Sent()) != 0 {
		t.Fatal("a message was sent for an already-verified address")
	}
}

func TestIssuingAgainInvalidatesTheEarlierLink(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	if err := env.svc.IssueFor(ctx, env.user); err != nil {
		t.Fatal(err)
	}
	first, _ := env.mailer.Last()

	if err := env.svc.IssueFor(ctx, env.user); err != nil {
		t.Fatal(err)
	}
	second, _ := env.mailer.Last()

	firstToken := tokenFromVerificationMail(t, first)
	secondToken := tokenFromVerificationMail(t, second)
	if firstToken == secondToken {
		t.Fatal("the second request reused the first token")
	}

	if _, err := env.svc.Verify(ctx, firstToken); err == nil {
		t.Fatal("the superseded link still works")
	}
	if _, err := env.svc.Verify(ctx, secondToken); err != nil {
		t.Fatalf("the newest link should work: %v", err)
	}
}

func TestIssueForAddressDoesNotRevealWhetherAnAccountExists(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	sent, err := env.svc.IssueForAddress(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("an unknown address must not error, got %v", err)
	}
	if sent {
		t.Fatal("nothing should be sent for an unknown address")
	}
	if len(env.mailer.Sent()) != 0 {
		t.Fatal("the mailer was used for an unknown address")
	}
}

func TestIssueForAddressReportsAlreadyVerified(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	if err := env.users.MarkEmailVerified(ctx, env.user.ID, env.now); err != nil {
		t.Fatal(err)
	}
	// Unlike an unknown address, this is worth reporting: the caller owns the address, so
	// it tells them nothing new and saves them waiting for a link that will not come.
	_, err := env.svc.IssueForAddress(ctx, "booker@example.com")
	if !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestVerifyTwiceKeepsTheOriginalTimestamp(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	first := env.now
	if err := env.users.MarkEmailVerified(ctx, env.user.ID, first); err != nil {
		t.Fatal(err)
	}
	// A second marking, later, must not move it.
	if err := env.users.MarkEmailVerified(ctx, env.user.ID, first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	u, err := env.users.GetUserByID(ctx, env.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.EmailVerifiedAt == nil {
		t.Fatal("expected a timestamp")
	}
	if !u.EmailVerifiedAt.Equal(first) {
		t.Fatalf("timestamp moved: %v -> %v", first, *u.EmailVerifiedAt)
	}
}

func TestVerifyWithoutAMailerStillIssuesAToken(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()
	// No mailer is the configuration-free case, and also how the resend path behaves when
	// mail is not set up: the token exists even though nothing was sent.
	svc := NewEmailVerification(env.users, env.tokens, nil, "https://example.test",
		DefaultVerificationTTL, func() time.Time { return env.now })

	if err := svc.IssueFor(ctx, env.user); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.tokens.UserTokenCount(env.user.ID) == 0 {
		t.Fatal("expected a token to have been created")
	}
}

func TestVerifyForADeletedAccountFailsAndClearsTheToken(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()

	if err := env.svc.IssueFor(ctx, env.user); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()
	token := tokenFromVerificationMail(t, msg)

	if err := env.users.DeleteUser(ctx, env.user.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := env.svc.Verify(ctx, token); err == nil {
		t.Fatal("expected verification to fail for a deleted account")
	}
	// No live token may remain, so this case is indistinguishable from an unknown token.
	if n := env.tokens.UserTokenCount(env.user.ID); n != 0 {
		t.Fatalf("expected the token to be cleared, %d remain", n)
	}
}

func TestVerifyReportsAMailFailure(t *testing.T) {
	env := newVerifyEnv(t)
	ctx := context.Background()
	env.mailer.FailWith(errors.New("provider is down"))

	// Unlike the reset request, a mail failure here is surfaced: the caller is a signed-in
	// user or a registration, both of which can act on it, and there is no enumeration
	// concern to protect on the IssueFor path.
	if err := env.svc.IssueFor(ctx, env.user); err == nil {
		t.Fatal("expected the mail failure to surface")
	}
}
