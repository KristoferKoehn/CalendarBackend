package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validMessage() Message {
	return Message{
		To:      "booker@example.com",
		Subject: "Confirm your address",
		Text:    "Click the link to confirm.",
	}
}

func TestMessageValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Message)
		wantErr bool
	}{
		{name: "valid", mutate: func(*Message) {}, wantErr: false},
		{name: "html only is enough", mutate: func(m *Message) { m.Text = ""; m.HTML = "<p>hi</p>" }, wantErr: false},
		{name: "missing recipient", mutate: func(m *Message) { m.To = "" }, wantErr: true},
		{name: "recipient is not an address", mutate: func(m *Message) { m.To = "nobody" }, wantErr: true},
		{name: "missing subject", mutate: func(m *Message) { m.Subject = "  " }, wantErr: true},
		{name: "no body at all", mutate: func(m *Message) { m.Text = ""; m.HTML = "" }, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validMessage()
			tc.mutate(&m)
			err := m.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestStubRecordsInsteadOfSending(t *testing.T) {
	stub := NewStub()
	ctx := context.Background()

	if err := stub.Send(ctx, validMessage()); err != nil {
		t.Fatal(err)
	}
	if err := stub.Send(ctx, Message{To: "second@example.com", Subject: "Second", Text: "body"}); err != nil {
		t.Fatal(err)
	}

	sent := stub.Sent()
	if len(sent) != 2 {
		t.Fatalf("expected 2 recorded messages, got %d", len(sent))
	}
	if sent[0].To != "booker@example.com" || sent[1].To != "second@example.com" {
		t.Fatalf("recorded in the wrong order: %+v", sent)
	}

	last, ok := stub.Last()
	if !ok || last.Subject != "Second" {
		t.Fatalf("unexpected last message: %+v (ok=%v)", last, ok)
	}

	stub.Reset()
	if len(stub.Sent()) != 0 {
		t.Fatal("expected Reset to clear the record")
	}
}

func TestStubRejectsInvalidMessages(t *testing.T) {
	stub := NewStub()
	if err := stub.Send(context.Background(), Message{To: "no-subject@example.com"}); err == nil {
		t.Fatal("expected validation to fail")
	}
	if _, ok := stub.Last(); ok {
		t.Fatal("an invalid message must not be recorded")
	}
}

func TestStubCanSimulateFailure(t *testing.T) {
	stub := NewStub()
	boom := errors.New("boom")
	stub.FailWith(boom)

	if err := stub.Send(context.Background(), validMessage()); !errors.Is(err, boom) {
		t.Fatalf("expected the injected error, got %v", err)
	}
	if _, ok := stub.Last(); ok {
		t.Fatal("a failed send must not be recorded")
	}
}

func TestResendSendsTheExpectedRequest(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody resendRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("could not decode the request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"8f1d2c3b-0000-4000-8000-000000000001"}`))
	}))
	defer server.Close()

	r := NewResend("re_test_key", "CalendarSite <no-reply@example.com>", "hello@example.com")
	r.Endpoint = server.URL + "/emails"

	if err := r.Send(context.Background(), validMessage()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/emails" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotAuth != "Bearer re_test_key" {
		t.Fatalf("unexpected Authorization header %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("unexpected Content-Type %q", gotContentType)
	}
	if gotBody.From != "CalendarSite <no-reply@example.com>" {
		t.Fatalf("unexpected From %q", gotBody.From)
	}
	if len(gotBody.To) != 1 || gotBody.To[0] != "booker@example.com" {
		t.Fatalf("unexpected To %v", gotBody.To)
	}
	if gotBody.Subject != "Confirm your address" || gotBody.Text != "Click the link to confirm." {
		t.Fatalf("unexpected subject or body: %+v", gotBody)
	}
	// An empty HTML part must be omitted rather than sent as "".
	if gotBody.HTML != "" {
		t.Fatalf("expected no HTML, got %q", gotBody.HTML)
	}
	if gotBody.ReplyTo != "hello@example.com" {
		t.Fatalf("expected the configured Reply-To, got %q", gotBody.ReplyTo)
	}
}

func TestResendPerMessageReplyToOverridesTheDefault(t *testing.T) {
	var gotBody resendRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer server.Close()

	r := NewResend("key", "from@example.com", "default@example.com")
	r.Endpoint = server.URL

	m := validMessage()
	m.ReplyTo = "special@example.com"
	if err := r.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if gotBody.ReplyTo != "special@example.com" {
		t.Fatalf("expected the per-message Reply-To, got %q", gotBody.ReplyTo)
	}
}

func TestResendSurfacesProviderErrors(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		wantDetail    string
		wantRetryable bool
	}{
		{
			name: "invalid api key", status: 401,
			body:       `{"statusCode":401,"name":"validation_error","message":"API key is invalid"}`,
			wantDetail: "validation_error: API key is invalid", wantRetryable: false,
		},
		{
			name: "unverified domain", status: 403,
			body:       `{"statusCode":403,"name":"validation_error","message":"Domain not verified"}`,
			wantDetail: "validation_error: Domain not verified", wantRetryable: false,
		},
		{
			name: "rate limited", status: 429,
			body:       `{"statusCode":429,"name":"rate_limit_exceeded","message":"Too many requests"}`,
			wantDetail: "rate_limit_exceeded: Too many requests", wantRetryable: true,
		},
		{
			name: "provider fault", status: 503,
			body:       `{"message":"Service unavailable"}`,
			wantDetail: "Service unavailable", wantRetryable: true,
		},
		{
			name: "non-json body is still reported", status: 502,
			body:       `<html>bad gateway</html>`,
			wantDetail: "<html>bad gateway</html>", wantRetryable: true,
		},
		{
			name: "empty body", status: 400,
			body:       ``,
			wantDetail: "no error detail returned", wantRetryable: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			r := NewResend("key", "from@example.com", "")
			r.Endpoint = server.URL

			err := r.Send(context.Background(), validMessage())
			if err == nil {
				t.Fatal("expected an error")
			}
			var sendErr *SendError
			if !errors.As(err, &sendErr) {
				t.Fatalf("expected a *SendError, got %T", err)
			}
			if sendErr.StatusCode != tc.status {
				t.Fatalf("expected status %d, got %d", tc.status, sendErr.StatusCode)
			}
			if !strings.Contains(sendErr.Error(), tc.wantDetail) {
				t.Fatalf("expected the detail %q in %q", tc.wantDetail, sendErr.Error())
			}
			if sendErr.Retryable() != tc.wantRetryable {
				t.Fatalf("Retryable() = %v, want %v", sendErr.Retryable(), tc.wantRetryable)
			}
		})
	}
}

func TestResendRefusesToSendWithoutConfiguration(t *testing.T) {
	cases := []struct {
		name string
		r    *Resend
	}{
		{name: "no api key", r: NewResend("", "from@example.com", "")},
		{name: "no from address", r: NewResend("key", "", "")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A misconfiguration must fail before any request is made, which is what
			// makes this checkable without a server.
			err := tc.r.Send(context.Background(), validMessage())
			if err == nil {
				t.Fatal("expected an error")
			}
			var sendErr *SendError
			if !errors.As(err, &sendErr) {
				t.Fatalf("expected a *SendError, got %T", err)
			}
			if sendErr.Retryable() {
				t.Fatal("a configuration error is not retryable")
			}
		})
	}
}

func TestResendRejectsInvalidMessagesBeforeSending(t *testing.T) {
	// Point at a server that fails the test if it is ever reached.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was made for an invalid message")
	}))
	defer server.Close()

	r := NewResend("key", "from@example.com", "")
	r.Endpoint = server.URL

	if err := r.Send(context.Background(), Message{To: "x@example.com"}); err == nil {
		t.Fatal("expected validation to fail")
	}
}

func TestResendReportsTransportFailures(t *testing.T) {
	// Bind a port and immediately release it, so the address is well-formed but
	// nothing is listening. That yields a genuine connection error rather than a
	// status code, which is the case under test. (httptest.NewServer *starts* a
	// server, so closing one is not a way to simulate this.)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	r := NewResend("key", "from@example.com", "")
	r.Endpoint = "http://" + addr

	gotErr := r.Send(context.Background(), validMessage())
	if gotErr == nil {
		t.Fatal("expected a transport error")
	}

	var sendErr *SendError
	if !errors.As(gotErr, &sendErr) {
		t.Fatalf("expected a *SendError, got %T", gotErr)
	}
	if sendErr.StatusCode != 0 {
		t.Fatalf("a transport failure has no status, got %d", sendErr.StatusCode)
	}
	if !sendErr.Retryable() {
		t.Fatal("a transport failure should be retryable: the message was likely never accepted")
	}
}
