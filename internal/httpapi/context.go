package httpapi

import (
	"context"
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/domain"
)

type userContextKey struct{}

func withUser(ctx context.Context, u domain.User) context.Context {
	return context.WithValue(ctx, userContextKey{}, u)
}

func userFrom(ctx context.Context) (domain.User, bool) {
	u, ok := ctx.Value(userContextKey{}).(domain.User)
	return u, ok
}

// actorFrom returns the authenticated user for this request. Routes wrapped by
// authenticate always have one.
func actorFrom(r *http.Request) domain.User {
	u, _ := userFrom(r.Context())
	return u
}
