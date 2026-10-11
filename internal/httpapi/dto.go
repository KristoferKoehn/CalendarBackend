package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
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
}

type createAppointmentRequest struct {
	ResourceID string `json:"resourceId"`
	Date       string `json:"date"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
}

type rescheduleAppointmentRequest struct {
	Date      string `json:"date"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
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
