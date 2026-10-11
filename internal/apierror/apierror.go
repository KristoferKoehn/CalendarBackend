// Package apierror defines the error codes, HTTP status mapping, and the
// response envelope shared by the REST and MCP layers.
package apierror

import "net/http"

// Code is one of the six contract error codes.
type Code string

const (
	CodeInvalidInput    Code = "INVALID_INPUT"
	CodeUnauthenticated Code = "UNAUTHENTICATED"
	CodeForbidden       Code = "FORBIDDEN"
	CodeNotFound        Code = "NOT_FOUND"
	CodeConflict        Code = "CONFLICT"
	CodeInternal        Code = "INTERNAL"
)

// Error is a contract error carrying its HTTP status and optional details.
type Error struct {
	Code    Code
	Message string
	Details any
	Status  int
}

func (e *Error) Error() string { return e.Message }

func InvalidInput(msg string) *Error {
	return &Error{Code: CodeInvalidInput, Message: msg, Status: http.StatusBadRequest}
}

func Unauthenticated(msg string) *Error {
	return &Error{Code: CodeUnauthenticated, Message: msg, Status: http.StatusUnauthorized}
}

func Forbidden(msg string) *Error {
	return &Error{Code: CodeForbidden, Message: msg, Status: http.StatusForbidden}
}

func NotFound(msg string) *Error {
	return &Error{Code: CodeNotFound, Message: msg, Status: http.StatusNotFound}
}

func Conflict(msg string) *Error {
	return &Error{Code: CodeConflict, Message: msg, Status: http.StatusConflict}
}

func Internal(msg string) *Error {
	return &Error{Code: CodeInternal, Message: msg, Status: http.StatusInternalServerError}
}

// WithDetails attaches optional details to the error.
func (e *Error) WithDetails(d any) *Error {
	e.Details = d
	return e
}

// Body is the error object inside the envelope.
type Body struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Envelope is the response body for every error.
type Envelope struct {
	Error Body `json:"error"`
}

// Envelope builds the wire format for this error.
func (e *Error) Envelope() Envelope {
	return Envelope{Error: Body{Code: e.Code, Message: e.Message, Details: e.Details}}
}
