package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store"
)

const protocolVersion = "2024-11-05"

// requestContextKey carries the inbound HTTP request so tool handlers can identify
// the caller for rate limiting, which is the only thing they need it for.
type requestContextKey struct{}

func withRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, requestContextKey{}, r)
}

func requestFrom(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(requestContextKey{}).(*http.Request)
	return r, ok
}

// Deps holds the dependencies for the MCP handler.
type Deps struct {
	Auth         *core.AuthService
	Resources    *core.ResourceService
	Appointments *core.AppointmentService
	Users        *core.UserService
	Sessions     store.SessionStore
	UserStore    store.UserStore
	Logger       *slog.Logger
	// Limiter and RateLimit apply the same sign-in and sign-up throttling as the
	// REST transport, so MCP cannot be used to bypass it. A nil Limiter disables
	// throttling.
	Limiter           *ratelimit.Limiter
	RateLimit         config.RateLimit
	TrustProxyHeaders bool
}

// Handler serves MCP over HTTP via JSON-RPC 2.0.
type Handler struct {
	auth              *core.AuthService
	resources         *core.ResourceService
	appointments      *core.AppointmentService
	users             *core.UserService
	sessions          store.SessionStore
	userStore         store.UserStore
	logger            *slog.Logger
	tools             []tool
	limiter           *ratelimit.Limiter
	rateLimit         config.RateLimit
	trustProxyHeaders bool
}

func NewHandler(d Deps) http.Handler {
	h := &Handler{
		auth:              d.Auth,
		resources:         d.Resources,
		appointments:      d.Appointments,
		users:             d.Users,
		sessions:          d.Sessions,
		userStore:         d.UserStore,
		logger:            d.Logger,
		limiter:           d.Limiter,
		rateLimit:         d.RateLimit,
		trustProxyHeaders: d.TrustProxyHeaders,
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	h.tools = h.buildTools()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defer func() {
		if rec := recover(); rec != nil {
			h.logger.Error("mcp panic", "panic", rec)
		}
	}()

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, response{JSONRPC: "2.0", Error: &rpcError{Code: errParse, Message: "parse error"}})
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPC(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: errInvalidReq, Message: "invalid request"}})
		return
	}

	if isNotification(req) {
		return
	}

	// Stash the request so tool handlers can identify the caller for throttling.
	ctx := withRequest(r.Context(), r)
	writeRPC(w, h.dispatch(r.WithContext(ctx), req))
}

func isNotification(req request) bool {
	return len(req.ID) == 0 || string(req.ID) == "null"
}

func (h *Handler) dispatch(r *http.Request, req request) response {
	switch req.Method {
	case "initialize":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "CalendarBackend", "version": "1.0.0"},
		}}
	case "tools/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": h.toolDefs()}}
	case "tools/call":
		return h.callTool(r, req)
	case "ping":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: errMethodNotFound, Message: "method not found"}}
	}
}

func (h *Handler) callTool(r *http.Request, req request) response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: errInvalidParams, Message: "invalid params"}}
	}

	t, ok := h.findTool(params.Name)
	if !ok {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: errInvalidParams, Message: "unknown tool: " + params.Name, Data: map[string]any{"code": "INVALID_INPUT"}}}
	}

	var actor domain.User
	if !t.public {
		u, err := h.authenticate(r)
		if err != nil {
			return response{JSONRPC: "2.0", ID: req.ID, Error: rpcErrorFrom(err)}
		}
		actor = u
	}

	args := params.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}

	out, err := t.run(r.Context(), actor, args)
	if err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: rpcErrorFrom(err)}
	}

	text, err := json.Marshal(out)
	if err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: errInternal, Message: "internal error"}}
	}

	return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
		"isError": false,
	}}
}

func (h *Handler) authenticate(r *http.Request) (domain.User, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return domain.User{}, apierror.Unauthenticated("Missing token")
	}
	sess, err := h.sessions.GetSessionByTokenHash(r.Context(), auth.HashToken(token))
	if err != nil {
		return domain.User{}, apierror.Unauthenticated("Expired or invalid token")
	}
	if time.Now().After(sess.ExpiresAt) {
		return domain.User{}, apierror.Unauthenticated("Expired or invalid token")
	}
	user, err := h.userStore.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		return domain.User{}, apierror.Unauthenticated("Expired or invalid token")
	}
	return user, nil
}

func (h *Handler) toolDefs() []map[string]any {
	out := make([]map[string]any, 0, len(h.tools))
	for _, t := range h.tools {
		out = append(out, map[string]any{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.inputSchema,
			// Published, because a caller cannot otherwise tell which tools are callable without a
			// token. Without it every well-behaved client has to attempt the call and read the
			// refusal, and anything else describing the surface has to guess from the tool's name —
			// which is how the generated documentation came to hardcode the two public tools.
			"public": t.public,
		})
	}
	return out
}

func (h *Handler) findTool(name string) (tool, bool) {
	for _, t := range h.tools {
		if t.name == name {
			return t, true
		}
	}
	return tool{}, false
}

func writeRPC(w http.ResponseWriter, resp response) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func rpcErrorFrom(err error) *rpcError {
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		return &rpcError{
			Code:    errToolExecution,
			Message: apiErr.Message,
			Data:    map[string]any{"code": string(apiErr.Code)},
		}
	}
	return &rpcError{Code: errInternal, Message: "internal error"}
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
