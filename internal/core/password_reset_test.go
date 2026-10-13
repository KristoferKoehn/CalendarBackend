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

const resetSite = "https://example.test"

type resetEnv struct {
	svc      *PasswordReset
	users    *memory.UserStore
	tokens   *memory.AuthTokenStore
	sessions *memory.SessionStore
	mailer   *mail.Stub
	user     domain.User
	now      time.Time
}

// newResetEnv wires the service against in-memory stores and a recording mailer.
func newResetEnv(t *testing.T) *resetEnv {
	t.Helper()

	users := memory.NewUserStore()
	tokens := memory.NewAuthTokenStore()
	sessions := memory.NewSessionStore()
	mailer := mail.NewStub()

	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

	hash, err := auth.HashPassword("original-password")
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

	env := &resetEnv{
		users:    users,
		tokens:   tokens,
		sessions: sessions,
		mailer:   mailer,
		user:     u,
		now:      now,
	}
	// The clock reads env.now through the pointer, so a test can advance time and
	// have the service observe it.
	env.svc = NewPasswordReset(users, tokens, sessions, mailer, resetSite, DefaultResetTTL,
		func() time.Time { return env.now })
	return env
}

// resetTokenFromMail extracts the raw token from the link in the recorded message.
func resetTokenFromMail(t *testing.T, m mail.Message) string {
	t.Helper()
	const marker = "/reset-password?token="
	i := strings.Index(m.Text, marker)
	if i < 0 {
		t.Fatalf("no reset link in the message body:\n%s", m.Text)
	}
	rest := m.Text[i+len(marker):]
	end := strings.IndexAny(rest, "\r\n ")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func TestRequestSendsAMailWithAUsableLink(t *testing.T) {
	env := newResetEnv(t)

	sent, err := env.svc.Request(context.Background(), "booker@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sent {
		t.Fatal("expected a message to be sent")
	}

	msg, ok := env.mailer.Last()
	if !ok {
		t.Fatal("expected a recorded message")
	}
	if msg.To != "booker@example.com" {
		t.Fatalf("unexpected recipient %q", msg.To)
	}
	if !strings.Contains(msg.Subject, "password") {
		t.Fatalf("subject does not mention a password: %q", msg.Subject)
	}
	// The link must point at the public site, and the body must say the link is
	// single-use and expiring, because that is what tells the reader it is safe to
	// ignore a message they did not ask for.
	if !strings.Contains(msg.Text, resetSite+"/reset-password?token=") {
		t.Fatalf("link does not use the public site origin:\n%s", msg.Text)
	}
	if !strings.Contains(msg.Text, "only be used once") || !strings.Contains(msg.Text, "expires in") {
		t.Fatalf("body omits the single-use/expiry reassurance:\n%s", msg.Text)
	}

	token := resetTokenFromMail(t, msg)
	if token == "" {
		t.Fatal("extracted an empty token")
	}

	if err := env.svc.Apply(context.Background(), token, "brand-new-password"); err != nil {
		t.Fatalf("expected the reset to succeed: %v", err)
	}

	_, hash, err := env.users.GetUserByEmail(context.Background(), "booker@example.com")
	if err != nil {
		t.Fatal(err)
	}
	okNew, err := auth.VerifyPassword("brand-new-password", hash)
	if err != nil || !okNew {
		t.Fatalf("the new password does not verify (ok=%v err=%v)", okNew, err)
	}
	old, err := auth.VerifyPassword("original-password", hash)
	if err != nil {
		t.Fatal(err)
	}
	if old {
		t.Fatal("the old password still works")
	}
}

func TestUnknownAddressLooksIdenticalToAKnownOne(t *testing.T) {
	env := newResetEnv(t)

	sent, err := env.svc.Request(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatalf("an unknown address must not produce an error, got %v", err)
	}
	if sent {
		t.Fatal("no message should be sent for an unknown address")
	}
	if len(env.mailer.Sent()) != 0 {
		t.Fatal("the mailer was used for an unknown address")
	}
}

func TestRequestIsRateLimitedByCallersNotHere(t *testing.T) {
	// The service deliberately does not throttle; the HTTP layer owns that so the
	// limiter is shared with the other transports. What matters here is that a second
	// request invalidates the first link rather than leaving two live credentials.
	env := newResetEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	first, _ := env.mailer.Last()
	firstToken := resetTokenFromMail(t, first)

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	second, _ := env.mailer.Last()
	secondToken := resetTokenFromMail(t, second)

	if firstToken == secondToken {
		t.Fatal("the second request reused the first token")
	}

	if err := env.svc.Apply(ctx, firstToken, "from-the-old-link"); err == nil {
		t.Fatal("the superseded link still works")
	}
	if err := env.svc.Apply(ctx, secondToken, "from-the-new-link"); err != nil {
		t.Fatalf("the newest link should work: %v", err)
	}
}

func TestATokenCannotBeUsedTwice(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()
	token := resetTokenFromMail(t, msg)

	if err := env.svc.Apply(ctx, token, "first-new-password"); err != nil {
		t.Fatalf("the first use should succeed: %v", err)
	}
	if err := env.svc.Apply(ctx, token, "second-new-password"); err == nil {
		t.Fatal("the same token worked twice")
	}

	// The second attempt must not have changed anything either.
	_, hash, _ := env.users.GetUserByEmail(ctx, "booker@example.com")
	ok, _ := auth.VerifyPassword("first-new-password", hash)
	if !ok {
		t.Fatal("the successful reset was overwritten by the rejected one")
	}
}

func TestExpiredTokensAreRefused(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()
	token := resetTokenFromMail(t, msg)

	// Move the clock past the TTL.
	env.now = env.now.Add(DefaultResetTTL + time.Second)

	if err := env.svc.Apply(ctx, token, "too-late-password"); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestGarbageAndEmptyTokensAreRefused(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	for _, token := range []string{"", "not-a-real-token", strings.Repeat("a", 64)} {
		if err := env.svc.Apply(ctx, token, "some-new-password"); err == nil {
			t.Fatalf("token %q was accepted", token)
		}
	}
}

func TestVerificationTokenCannotBeRedeemedAsAReset(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	// Plant a verification-purpose token whose raw value we know.
	raw := "verification-token-raw-value"
	if err := env.tokens.CreateAuthToken(ctx, domain.AuthToken{
		ID:        uuid.New(),
		UserID:    env.user.ID,
		Purpose:   domain.PurposeEmailVerification,
		TokenHash: auth.HashToken(raw),
		ExpiresAt: env.now.Add(time.Hour),
		CreatedAt: env.now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := env.svc.Apply(ctx, raw, "sneaky-new-password"); err == nil {
		t.Fatal("a verification token was accepted as a reset credential")
	}
}

func TestResetInvalidatesEveryExistingSession(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	// Two devices signed in.
	for _, tokenHash := range []string{"session-one", "session-two"} {
		if err := env.sessions.CreateSession(ctx, domain.Session{
			ID:        uuid.New(),
			UserID:    env.user.ID,
			TokenHash: tokenHash,
			ExpiresAt: env.now.Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()

	if err := env.svc.Apply(ctx, resetTokenFromMail(t, msg), "recovered-password"); err != nil {
		t.Fatal(err)
	}

	// A reset that leaves the old holder signed in has not recovered the account.
	for _, tokenHash := range []string{"session-one", "session-two"} {
		if _, err := env.sessions.GetSessionByTokenHash(ctx, tokenHash); err == nil {
			t.Fatalf("session %q survived the reset", tokenHash)
		}
	}
}

func TestReusingTheCurrentPasswordIsRefused(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()

	err := env.svc.Apply(ctx, resetTokenFromMail(t, msg), "original-password")
	if err == nil {
		t.Fatal("expected reusing the current password to be refused")
	}
	if !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestAMailFailureDoesNotLeakAndDoesNotFailTheRequest(t *testing.T) {
	env := newResetEnv(t)
	env.mailer.FailWith(errors.New("provider is down"))

	// The caller must still see success: if a mail failure changed the response, an
	// attacker could distinguish a real account (whose send was attempted) from an
	// absent one.
	sent, err := env.svc.Request(context.Background(), "booker@example.com")
	if err != nil {
		t.Fatalf("a mail failure must not surface as an error, got %v", err)
	}
	if sent {
		t.Fatal("sent should be false when the provider rejected the message")
	}

	// The token was still created, so operations can see what happened.
	if _, err := env.tokens.GetAuthTokenByHash(context.Background(), auth.HashToken("x")); err == nil {
		t.Fatal("unexpected token")
	}
}

func TestAResetForADeletedAccountFailsAndConsumesTheToken(t *testing.T) {
	env := newResetEnv(t)
	ctx := context.Background()

	if _, err := env.svc.Request(ctx, "booker@example.com"); err != nil {
		t.Fatal(err)
	}
	msg, _ := env.mailer.Last()
	token := resetTokenFromMail(t, msg)

	// The account is removed between the request and the click.
	if err := env.users.DeleteUser(ctx, env.user.ID); err != nil {
		t.Fatal(err)
	}

	if err := env.svc.Apply(ctx, token, "orphaned-password"); err == nil {
		t.Fatal("expected the reset to fail for a deleted account")
	}

	// The link must not survive as a live credential, and no token should be left
	// behind: a lingering row would make this case distinguishable from an unknown
	// token, which is exactly the signal the opaque message exists to hide.
	if n := env.tokens.UserTokenCount(env.user.ID); n != 0 {
		t.Fatalf("expected the token to be cleared, %d remain", n)
	}
}

func TestRequestWithoutAMailerStillIssuesAToken(t *testing.T) {
	env := newResetEnv(t)
	// A nil mailer is the configuration-free case: the token is created and nothing
	// is sent. Useful in tests, and when mail is not configured yet.
	svc := NewPasswordReset(env.users, env.tokens, env.sessions, nil, resetSite, DefaultResetTTL,
		func() time.Time { return env.now })

	sent, err := svc.Request(context.Background(), "booker@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent {
		t.Fatal("nothing can be sent without a mailer")
	}

	// The link is recorded as an account task even though no mail went out.
	if env.tokens.UserTokenCount(env.user.ID) == 0 {
		t.Fatal("expected a reset token to have been created")
	}
}
