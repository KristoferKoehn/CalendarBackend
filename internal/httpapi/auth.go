package httpapi

import (
	"net/http"
	"strings"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
)

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
}

func (h *Handler) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, apierror.Unauthenticated("Missing token"))
			return
		}

		sess, err := h.sessions.GetSessionByTokenHash(r.Context(), auth.HashToken(token))
		if err != nil {
			writeError(w, apierror.Unauthenticated("Expired or invalid token"))
			return
		}
		if time.Now().After(sess.ExpiresAt) {
			writeError(w, apierror.Unauthenticated("Expired or invalid token"))
			return
		}

		user, err := h.userStore.GetUserByID(r.Context(), sess.UserID)
		if err != nil {
			writeError(w, apierror.Unauthenticated("Expired or invalid token"))
			return
		}

		next(w, r.WithContext(withUser(r.Context(), user)))
	}
}
