package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/projects"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store"
)

// Deps holds the dependencies for the HTTP handlers.
type Deps struct {
	Auth         *core.AuthService
	Resources    *core.ResourceService
	Users        *core.UserService
	Appointments *core.AppointmentService
	Sessions     store.SessionStore
	UserStore    store.UserStore
	Logger       *slog.Logger
	CORSOrigins  []string
	// Limiter and RateLimit guard the sign-in and sign-up endpoints. A nil Limiter
	// disables throttling, which is what the handler tests use.
	Limiter           *ratelimit.Limiter
	RateLimit         config.RateLimit
	TrustProxyHeaders bool
	// Mail sends transactional email. A nil Mailer means nothing can be sent, which
	// is the safe default for tests.
	Mailer mail.Sender
	// PublicSiteURL is the web app origin, used to build links in email.
	PublicSiteURL string
	// PasswordReset implements the forgotten-password flow. Nil disables the two
	// reset routes, which then answer as if every address were unknown.
	PasswordReset *core.PasswordReset
	// EmailVerification implements proving control of an address. Nil disables the two
	// verification routes.
	EmailVerification *core.EmailVerification
	// Projects is the portfolio store. Nil means projects are not configured on this deployment:
	// the list endpoint answers with nothing rather than failing, so the app still runs.
	Projects *projects.Store
}

// Handler carries the dependencies and serves the REST API.
type Handler struct {
	auth              *core.AuthService
	resources         *core.ResourceService
	users             *core.UserService
	appointments      *core.AppointmentService
	sessions          store.SessionStore
	userStore         store.UserStore
	logger            *slog.Logger
	limiter           *ratelimit.Limiter
	rateLimit         config.RateLimit
	trustProxyHeaders bool
	mailer            mail.Sender
	publicSiteURL     string
	passwordReset     *core.PasswordReset
	emailVerification *core.EmailVerification
	projects          *projects.Store
}

// NewHandler builds the routed, middleware-wrapped HTTP handler.
func NewHandler(d Deps) http.Handler {
	h := &Handler{
		auth:              d.Auth,
		resources:         d.Resources,
		users:             d.Users,
		appointments:      d.Appointments,
		sessions:          d.Sessions,
		userStore:         d.UserStore,
		logger:            d.Logger,
		limiter:           d.Limiter,
		rateLimit:         d.RateLimit,
		trustProxyHeaders: d.TrustProxyHeaders,
		mailer:            d.Mailer,
		publicSiteURL:     d.PublicSiteURL,
		passwordReset:     d.PasswordReset,
		emailVerification: d.EmailVerification,
		projects:          d.Projects,
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)

	mux.HandleFunc("POST /auth/register", h.register)
	mux.HandleFunc("POST /auth/login", h.login)
	// Password reset is unauthenticated by necessity, so both routes are rate limited
	// and both answer identically whether or not the address has an account.
	mux.HandleFunc("POST /auth/forgot-password", h.forgotPassword)
	mux.HandleFunc("POST /auth/reset-password", h.resetPassword)
	// Verification is reachable signed out, because the link is opened from a mail client
	// where there is no session.
	mux.HandleFunc("POST /auth/verify-email", h.verifyEmail)
	mux.HandleFunc("POST /auth/resend-verification", h.resendVerification)
	mux.HandleFunc("GET /auth/me", h.authenticate(h.me))
	mux.HandleFunc("PATCH /auth/me", h.authenticate(h.updateProfile))
	mux.HandleFunc("DELETE /auth/me", h.authenticate(h.deleteSelf))

	mux.HandleFunc("GET /resources", h.authenticate(h.listResources))
	mux.HandleFunc("POST /resources", h.authenticate(h.createResource))
	mux.HandleFunc("PATCH /resources/{id}", h.authenticate(h.updateResource))
	mux.HandleFunc("DELETE /resources/{id}", h.authenticate(h.deactivateResource))

	mux.HandleFunc("GET /users", h.authenticate(h.listDirectory))
	mux.HandleFunc("GET /users/{id}", h.authenticate(h.getUser))
	mux.HandleFunc("PATCH /users/{id}", h.authenticate(h.updateUser))
	mux.HandleFunc("DELETE /users/{id}", h.authenticate(h.deleteUser))

	mux.HandleFunc("POST /appointments", h.authenticate(h.createAppointment))
	mux.HandleFunc("GET /appointments", h.authenticate(h.listAppointments))
	mux.HandleFunc("DELETE /appointments/{id}", h.authenticate(h.deleteAppointment))
	mux.HandleFunc("POST /appointments/{id}/cancel", h.authenticate(h.cancelAppointment))
	mux.HandleFunc("POST /appointments/{id}/reschedule", h.authenticate(h.rescheduleAppointment))

	// Portfolio projects. Reading is public because the landing page shows them to everyone;
	// creating and changing is checked against the actor's role inside the store.
	mux.HandleFunc("GET /projects", h.listProjects)
	mux.HandleFunc("GET /projects/{slug}", h.getProject)
	mux.HandleFunc("POST /projects", h.authenticate(h.createProject))
	mux.HandleFunc("PUT /projects/{slug}", h.authenticate(h.updateProject))
	mux.HandleFunc("DELETE /projects/{slug}", h.authenticate(h.deleteProject))

	var handler http.Handler = mux
	handler = recoverPanics(h.logger, handler)
	handler = logRequests(h.logger, handler)
	handler = cors(d.CORSOrigins, h.logger, handler)
	return handler
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		// Advertise how long to wait when a request was throttled, so a client can
		// back off correctly instead of hammering.
		if apiErr.RetryAfter > 0 {
			seconds := int(apiErr.RetryAfter.Round(time.Second) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		writeJSON(w, apiErr.Status, apiErr.Envelope())
		return
	}
	slog.Default().Error("unexpected error", "error", err)
	writeJSON(w, http.StatusInternalServerError, apierror.Internal("unexpected error").Envelope())
}

func pathUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, apierror.InvalidInput("must be a valid UUID"))
		return uuid.Nil, false
	}
	return id, true
}
