package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultResendEndpoint is Resend's send-email endpoint. It is a field on the client
// rather than a constant so tests can point it at a local server.
const DefaultResendEndpoint = "https://api.resend.com/emails"

// maxErrorBody bounds how much of an error response is read, so a misconfigured
// endpoint returning a large HTML page cannot be pulled into memory or a log line.
const maxErrorBody = 2 << 10

// Resend sends through Resend's HTTP API.
//
// This talks to the documented endpoint directly rather than pulling in the official
// SDK, for the same reason internal/store uses pgx directly: one small, auditable
// dependency surface. If the payload ever needs to diverge from the documented
// shape, switching to the SDK is a change confined to this file.
type Resend struct {
	APIKey   string
	From     string
	ReplyTo  string
	Endpoint string
	Client   *http.Client
}

// NewResend builds a client with sensible transport settings.
func NewResend(apiKey, from, replyTo string) *Resend {
	return &Resend{
		APIKey:   apiKey,
		From:     from,
		ReplyTo:  replyTo,
		Endpoint: DefaultResendEndpoint,
		// A send that hangs must not hold a request goroutine open. Ten seconds is
		// far longer than a healthy send and short enough to fail usefully.
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (r *Resend) Name() string { return "resend" }

// resendRequest is the documented JSON body. Only fields we actually set are
// included, so an empty optional field is omitted rather than sent as "".
type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

type resendResponse struct {
	ID string `json:"id"`
}

type resendErrorBody struct {
	Message    string `json:"message"`
	Name       string `json:"name"`
	StatusCode int    `json:"statusCode"`
}

func (r *Resend) Send(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.APIKey) == "" {
		// A local misconfiguration, not a provider failure. Reported with a 4xx-ish
		// status so Retryable() is false: retrying an unconfigured sender is
		// pointless.
		return &SendError{StatusCode: http.StatusUnauthorized, Detail: "resend: API key is not configured"}
	}
	if strings.TrimSpace(r.From) == "" {
		return &SendError{StatusCode: http.StatusBadRequest, Detail: "resend: From address is not configured"}
	}

	replyTo := r.ReplyTo
	if strings.TrimSpace(m.ReplyTo) != "" {
		replyTo = m.ReplyTo
	}

	payload, err := json.Marshal(resendRequest{
		From:    r.From,
		To:      []string{m.To},
		Subject: m.Subject,
		Text:    m.Text,
		HTML:    m.HTML,
		ReplyTo: replyTo,
	})
	if err != nil {
		return fmt.Errorf("mail: encode request: %w", err)
	}

	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = DefaultResendEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mail: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		// A transport failure leaves StatusCode at zero, which Retryable() treats as
		// worth retrying: the message was probably never accepted.
		return &SendError{Detail: fmt.Sprintf("resend: %v", err)}
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &SendError{
			StatusCode: res.StatusCode,
			Detail:     readErrorDetail(res.Body),
		}
	}

	// Drain so the connection can be reused, and confirm the response is shaped as
	// expected without failing a send that the provider already accepted.
	var out resendResponse
	_ = json.NewDecoder(io.LimitReader(res.Body, maxErrorBody)).Decode(&out)
	return nil
}

// readErrorDetail extracts the provider's message, falling back to the raw body so
// an unexpected error shape is still visible in logs.
func readErrorDetail(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBody))
	if err != nil {
		return "could not read error response"
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "no error detail returned"
	}

	var parsed resendErrorBody
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Message != "" {
		if parsed.Name != "" {
			return parsed.Name + ": " + parsed.Message
		}
		return parsed.Message
	}
	return trimmed
}
