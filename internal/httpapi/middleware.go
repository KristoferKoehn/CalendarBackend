package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
)

// cors answers cross-origin requests for the origins in the allowlist.
//
// Takes a logger so a rejected origin is recorded. Without that, a browser refusing a response is
// invisible from the server side: the request arrives, the response goes out without the headers,
// and nothing anywhere says why. The value logged is the Origin the caller sent, which is exactly
// what has to be compared against the allowlist.
func cors(origins []string, logger *slog.Logger, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[normaliseOrigin(o)] = true
	}
	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Same-origin or a non-browser client such as curl. Nothing to allow.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Case and a trailing slash are not meaningful differences in an origin, so they are not
		// allowed to decide whether a request works.
		if allowed[normaliseOrigin(origin)] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		} else {
			// Logged at info because it is a configuration question, not a fault: either the
			// deployment's allowlist is missing an origin it should have, or something is
			// rewriting the header in transit.
			logger.Info("cross-origin request refused",
				"origin", origin,
				"method", r.Method,
				"path", r.URL.Path,
				"allowed", origins,
			)
		}

		if r.Method == http.MethodOptions {
			// Answered for every origin, allowed or not. A preflight is the browser asking
			// permission, and "no" is a complete answer: leaving it unanswered makes the browser
			// report a CORS failure with no status, which is harder to diagnose than a 204 that
			// simply lacks the headers.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// normaliseOrigin folds the differences that are not meaningful in an origin: case, and a trailing
// slash. A configured "https://Example.com/" and a browser sending "https://example.com" are the
// same origin and should not be treated as different.
func normaliseOrigin(origin string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(origin), "/"))
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start).String(),
		)
	})
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered", "panic", rec)
				writeError(w, apierror.Internal("unexpected error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
