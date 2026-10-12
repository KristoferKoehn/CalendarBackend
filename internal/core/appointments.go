package core

import (
	"context"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
	"kristoferkoehn/CalendarBackend/internal/timeutil"
)

// AppointmentService handles appointment use cases.
type AppointmentService struct {
	appointments store.AppointmentStore
	resources    store.ResourceStore
	now          func() time.Time
}

func NewAppointmentService(appointments store.AppointmentStore, resources store.ResourceStore, now func() time.Time) *AppointmentService {
	return &AppointmentService{
		appointments: appointments,
		resources:    resources,
		now:          orNow(now),
	}
}

func (s *AppointmentService) Create(ctx context.Context, actor domain.User, in CreateAppointmentInput) (domain.Appointment, error) {
	resource, err := s.resources.GetResourceByID(ctx, in.ResourceID)
	if err != nil {
		return domain.Appointment{}, err
	}
	if !resource.Active {
		return domain.Appointment{}, apierror.NotFound("Resource not found or inactive")
	}

	start, end, err := timeutil.ResolveRange(in.Date, in.StartTime, in.EndTime)
	if err != nil {
		return domain.Appointment{}, apierror.InvalidInput(err.Error())
	}

	overlap, err := s.appointments.HasOverlap(ctx, in.ResourceID, start, end, uuid.Nil)
	if err != nil {
		return domain.Appointment{}, err
	}
	if overlap {
		return domain.Appointment{}, apierror.Conflict("Overlapping booking")
	}

	now := s.now()
	a := domain.Appointment{
		ID:           uuid.New(),
		UserID:       actor.ID,
		ResourceID:   resource.ID,
		Title:        in.Title,
		Description:  in.Description,
		StartAt:      start,
		EndAt:        end,
		StartAtLocal: timeutil.FormatLocal(start),
		EndAtLocal:   timeutil.FormatLocal(end),
		Status:       domain.StatusBooked,
		CreatedAt:    now,
		UpdatedAt:    now,
		CancelledAt:  nil,
	}
	return s.appointments.CreateAppointment(ctx, a)
}

func (s *AppointmentService) List(ctx context.Context, actor domain.User, in AppointmentListInput) ([]domain.Appointment, error) {
	filter := store.AppointmentFilter{
		ResourceID: in.ResourceID,
		Title:      in.Title,
		Status:     in.Status,
	}

	if actor.Role == domain.RoleAdmin {
		filter.UserID = in.UserID
	} else {
		filter.UserID = &actor.ID
	}

	if in.From != nil {
		start, err := timeutil.DayStart(*in.From)
		if err != nil {
			return nil, apierror.InvalidInput(err.Error())
		}
		filter.From = &start
	}
	if in.To != nil {
		end, err := timeutil.NextDayStart(*in.To)
		if err != nil {
			return nil, apierror.InvalidInput(err.Error())
		}
		filter.To = &end
	}

	return s.appointments.ListAppointments(ctx, filter)
}

func (s *AppointmentService) Cancel(ctx context.Context, actor domain.User, id uuid.UUID) (domain.Appointment, error) {
	a, err := s.appointments.GetAppointmentByID(ctx, id)
	if err != nil {
		return domain.Appointment{}, err
	}
	if !canModifyAppointment(actor, a) {
		return domain.Appointment{}, apierror.Forbidden("Not the owner or an admin")
	}
	if a.Status == domain.StatusCancelled {
		return domain.Appointment{}, apierror.Conflict("Already cancelled")
	}
	return s.appointments.CancelAppointment(ctx, id, s.now())
}

func (s *AppointmentService) Reschedule(ctx context.Context, actor domain.User, id uuid.UUID, in RescheduleAppointmentInput) (domain.Appointment, error) {
	a, err := s.appointments.GetAppointmentByID(ctx, id)
	if err != nil {
		return domain.Appointment{}, err
	}
	if !canModifyAppointment(actor, a) {
		return domain.Appointment{}, apierror.Forbidden("Not the owner or an admin")
	}
	if a.Status == domain.StatusCancelled {
		return domain.Appointment{}, apierror.Conflict("Already cancelled")
	}

	start, end, err := timeutil.ResolveRange(in.Date, in.StartTime, in.EndTime)
	if err != nil {
		return domain.Appointment{}, apierror.InvalidInput(err.Error())
	}

	overlap, err := s.appointments.HasOverlap(ctx, a.ResourceID, start, end, a.ID)
	if err != nil {
		return domain.Appointment{}, err
	}
	if overlap {
		return domain.Appointment{}, apierror.Conflict("Overlapping booking")
	}

	return s.appointments.RescheduleAppointment(ctx, store.RescheduleParams{
		ID:          id,
		Title:       in.Title,
		Description: in.Description,
		Start:       start,
		End:         end,
		UpdatedAt:   s.now(),
	})
}
