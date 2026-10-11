package core

import (
	"context"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

const defaultTimezone = "America/Los_Angeles"

// ResourceService handles resource use cases.
type ResourceService struct {
	resources store.ResourceStore
}

func NewResourceService(resources store.ResourceStore) *ResourceService {
	return &ResourceService{resources: resources}
}

func (s *ResourceService) List(ctx context.Context, actor domain.User) ([]domain.Resource, error) {
	return s.resources.ListActiveResources(ctx)
}

func (s *ResourceService) Create(ctx context.Context, actor domain.User, in CreateResourceInput) (domain.Resource, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.Resource{}, err
	}

	timezone := defaultTimezone
	if in.Timezone != nil {
		timezone = *in.Timezone
	}

	r := domain.Resource{
		ID:          uuid.New(),
		Name:        in.Name,
		Description: in.Description,
		Timezone:    timezone,
		Active:      true,
		CreatedAt:   time.Now().UTC(),
	}
	return s.resources.CreateResource(ctx, r)
}

func (s *ResourceService) Update(ctx context.Context, actor domain.User, id uuid.UUID, in UpdateResourceInput) (domain.Resource, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.Resource{}, err
	}

	current, err := s.resources.GetResourceByID(ctx, id)
	if err != nil {
		return domain.Resource{}, err
	}

	if in.Name != nil {
		current.Name = *in.Name
	}
	if in.Description != nil {
		current.Description = in.Description
	}
	if in.Timezone != nil {
		current.Timezone = *in.Timezone
	}

	return s.resources.UpdateResource(ctx, current)
}

func (s *ResourceService) Deactivate(ctx context.Context, actor domain.User, id uuid.UUID) (domain.Resource, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.Resource{}, err
	}
	return s.resources.DeactivateResource(ctx, id)
}
