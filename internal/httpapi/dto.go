package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

// registerRequest is the whole of signing up: an address and a password.
//
// Nothing else. Details are collected on the screen the confirmation link leads to, because
// the address has to be proven before the account is worth describing, and because a
// two-field sign-up is one somebody finishes.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// forgotPasswordRequest starts a reset. The response never varies with whether the
// address has an account.
type forgotPasswordRequest struct {
	Email string `json:"email"`
}

// resetPasswordRequest redeems a reset link and sets the new password.
type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

// verifyEmailRequest redeems a verification link. No session is required: the link is
// opened from a mail client.
type verifyEmailRequest struct {
	Token string `json:"token"`
}

// resendVerificationRequest asks for a fresh verification link.
type resendVerificationRequest struct {
	Email string `json:"email"`
}

type createResourceRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Timezone    *string `json:"timezone"`
}

type updateResourceRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Timezone    *string `json:"timezone"`
}

type updateUserRequest struct {
	Role    *string `json:"role"`
	IsAgent *bool   `json:"isAgent"`
	// EmailVerified sets or clears the verification stamp. A pointer so that omitting it
	// leaves verification alone rather than silently clearing it.
	//
	// A boolean rather than a timestamp, because the realistic reason an admin touches this
	// is that somebody cannot receive the mail. The moment it was proven is the server's to
	// record, and inventing one here would be recording a confirmation that never happened.
	EmailVerified *bool `json:"emailVerified"`
}

type createAppointmentRequest struct {
	ResourceID  string `json:"resourceId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Date        string `json:"date"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
}

type rescheduleAppointmentRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Date        string `json:"date"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
}

type registerResponse struct {
	UserID string `json:"userId"`
}

type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type resourcesResponse struct {
	Resources []domain.Resource `json:"resources"`
}

type usersResponse struct {
	Users []domain.User `json:"users"`
}

type appointmentsResponse struct {
	Appointments []domain.Appointment `json:"appointments"`
}

func decodeJSON(r *http.Request, dst any) error {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return apierror.InvalidInput("invalid JSON body")
	}
	return nil
}

// decodeRawFields decodes a request body into a presence map, so a field that is
// absent can be told apart from one explicitly set to null. That distinction
// matters for the profile endpoint: absent means "leave alone", null means "clear".
func decodeRawFields(r *http.Request) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, apierror.InvalidInput("invalid JSON body")
	}
	if raw == nil {
		return nil, apierror.InvalidInput("body must be a JSON object")
	}
	return raw, nil
}

// profileTextField reads an optional nullable text field.
//
// absent  -> (nil, nil)          leave the stored value alone
// null    -> (&"", nil)          clear the stored value
// string  -> (&value, nil)       set it, and empty means the same as null
func profileTextField(raw map[string]json.RawMessage, key string, maxLen int, label string) (*string, error) {
	value, present := raw[key]
	if !present {
		return nil, nil
	}
	if isJSONNull(value) {
		empty := ""
		return &empty, nil
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return nil, apierror.InvalidInput(label + " must be a string or null")
	}
	if len(s) > maxLen {
		return nil, apierror.InvalidInput(label + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return &s, nil
}

// profileBirthdayField reads the optional birthday pair.
//
// absent  -> (nil, nil, nil)     leave it alone
// null    -> (&0, &0, nil)       clear it
// numbers -> (&month, &day, nil) set it
func profileBirthdayField(raw map[string]json.RawMessage) (*int, *int, error) {
	monthRaw, monthPresent := raw["birthMonth"]
	dayRaw, dayPresent := raw["birthDay"]

	if !monthPresent && !dayPresent {
		return nil, nil, nil
	}
	// Clearing is expressed by setting both to null.
	if monthPresent && dayPresent && isJSONNull(monthRaw) && isJSONNull(dayRaw) {
		zero := 0
		return &zero, &zero, nil
	}
	if !monthPresent || !dayPresent {
		return nil, nil, apierror.InvalidInput("birthMonth and birthDay must be provided together")
	}
	if isJSONNull(monthRaw) != isJSONNull(dayRaw) {
		return nil, nil, apierror.InvalidInput("birthMonth and birthDay must be provided together")
	}
	var month, day int
	if err := json.Unmarshal(monthRaw, &month); err != nil {
		return nil, nil, apierror.InvalidInput("birthMonth must be a number or null")
	}
	if err := json.Unmarshal(dayRaw, &day); err != nil {
		return nil, nil, apierror.InvalidInput("birthDay must be a number or null")
	}
	return &month, &day, nil
}

func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 4 && string(trimmed) == "null"
}

func validateAppointmentTimes(date, start, end string) error {
	if err := validation.Date(date); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	if err := validation.Time(start); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	if err := validation.Time(end); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	if err := validation.TimeRange(start, end); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	return nil
}
