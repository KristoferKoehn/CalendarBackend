package mcp

import (
	"context"
	"encoding/json"

	"kristoferkoehn/CalendarBackend/internal/domain"
)

type tool struct {
	name        string
	description string
	inputSchema map[string]any
	public      bool
	run         func(ctx context.Context, actor domain.User, args json.RawMessage) (any, error)
}

func (h *Handler) buildTools() []tool {
	return []tool{
		{
			name:        "auth.register",
			description: "Register a new user account.",
			inputSchema: objectSchema(map[string]any{"email": strProp(), "password": strProp()}, "email", "password"),
			public:      true,
			run:         h.runAuthRegister,
		},
		{
			name:        "auth.login",
			description: "Log in and receive a bearer token.",
			inputSchema: objectSchema(map[string]any{"email": strProp(), "password": strProp()}, "email", "password"),
			public:      true,
			run:         h.runAuthLogin,
		},
		{
			name:        "resources.list",
			description: "List active resources.",
			inputSchema: objectSchema(map[string]any{}),
			public:      false,
			run:         h.runResourcesList,
		},
		{
			name:        "appointments.create",
			description: "Book an appointment.",
			inputSchema: objectSchema(map[string]any{
				"resourceId": strProp(), "title": strProp(), "description": strProp(), "date": strProp(), "startTime": strProp(), "endTime": strProp(),
			}, "resourceId", "title", "date", "startTime", "endTime"),
			public: false,
			run:    h.runAppointmentsCreate,
		},
		{
			name:        "appointments.cancel",
			description: "Cancel an appointment.",
			inputSchema: objectSchema(map[string]any{"appointmentId": strProp()}, "appointmentId"),
			public:      false,
			run:         h.runAppointmentsCancel,
		},
		{
			name:        "appointments.reschedule",
			description: "Reschedule an appointment.",
			inputSchema: objectSchema(map[string]any{
				"appointmentId": strProp(), "title": strProp(), "description": strProp(), "date": strProp(), "startTime": strProp(), "endTime": strProp(),
			}, "appointmentId", "title", "date", "startTime", "endTime"),
			public: false,
			run:    h.runAppointmentsReschedule,
		},
		{
			name:        "appointments.delete",
			description: "Permanently delete an appointment. Admin accounts only.",
			inputSchema: objectSchema(map[string]any{"appointmentId": strProp()}, "appointmentId"),
			public:      false,
			run:         h.runAppointmentsDelete,
		},
		{
			name:        "appointments.list",
			description: "List appointments with optional filters. Appointments are visible to every authenticated user, not only their owner.",
			inputSchema: objectSchema(map[string]any{"resourceId": strProp(), "userId": strProp(), "title": strProp(), "status": strProp(), "from": strProp(), "to": strProp()}),
			public:      false,
			run:         h.runAppointmentsList,
		},
		{
			name: "users.delete",
			// The restrictions are stated because a caller cannot discover them otherwise: they are
			// enforced in the service, so the only feedback is a refusal after the fact.
			description: "Permanently delete a user account. Admin accounts only. An admin cannot delete their own account, and the last remaining admin cannot be deleted. The account's appointments are kept.",
			inputSchema: objectSchema(map[string]any{"userId": strProp()}, "userId"),
			public:      false,
			run:         h.runUsersDelete,
		},
	}
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp() map[string]any {
	return map[string]any{"type": "string"}
}
