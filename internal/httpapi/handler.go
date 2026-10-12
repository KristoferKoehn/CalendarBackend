package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/core"
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
}

// Handler carries the dependencies and serves the REST API.
type Handler struct {
	auth         *core.AuthService
	resources    *core.ResourceService
	users        *core.UserService
	appointments *core.AppointmentService
	sessions     store.SessionStore
	userStore    store.UserStore
	logger       *slog.Logger
}

// NewHandler builds the routed, middleware-wrapped HTTP handler.
func NewHandler(d Deps) http.Handler {
	h := &Handler{
		auth:         d.Auth,
		resources:    d.Resources,
		users:        d.Users,
		appointments: d.Appointments,
		sessions:     d.Sessions,
		userStore:    d.UserStore,
		logger:       d.Logger,
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)

	mux.HandleFunc("POST /auth/register", h.register)
	mux.HandleFunc("POST /auth/login", h.login)
	mux.HandleFunc("GET /auth/me", h.authenticate(h.me))
	mux.HandleFunc("DELETE /auth/me", h.authenticate(h.deleteSelf))

	mux.HandleFunc("GET /resources", h.authenticate(h.listResources))
	mux.HandleFunc("POST /resources", h.authenticate(h.createResource))
	mux.HandleFunc("PATCH /resources/{id}", h.authenticate(h.updateResource))
	mux.HandleFunc("DELETE /resources/{id}", h.authenticate(h.deactivateResource))

	mux.HandleFunc("GET /users", h.authenticate(h.listUsers))
	mux.HandleFunc("PATCH /users/{id}", h.authenticate(h.updateUser))
	mux.HandleFunc("DELETE /users/{id}", h.authenticate(h.deleteUser))

	mux.HandleFunc("POST /appointments", h.authenticate(h.createAppointment))
	mux.HandleFunc("GET /appointments", h.authenticate(h.listAppointments))
	mux.HandleFunc("DELETE /appointments/{id}", h.authenticate(h.deleteAppointment))
	mux.HandleFunc("POST /appointments/{id}/cancel", h.authenticate(h.cancelAppointment))
	mux.HandleFunc("POST /appointments/{id}/reschedule", h.authenticate(h.rescheduleAppointment))

	var handler http.Handler = mux
	handler = recoverPanics(h.logger, handler)
	handler = logRequests(h.logger, handler)
	handler = cors(d.CORSOrigins, handler)
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
