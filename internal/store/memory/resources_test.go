package memory

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/domain"
)

func TestListActiveResourcesIsAlphabetical(t *testing.T) {
	ctx := context.Background()
	store := NewResourceStore()

	// Created out of order, and with mixed case on purpose: ASCII ordering would put
	// "Zebra" before "atrium", which is exactly the trap this test exists for.
	for _, name := range []string{"Zebra Room", "atrium", "Boardroom", "breakout"} {
		r := domain.Resource{
			ID: uuid.New(), Name: name, Timezone: "America/Los_Angeles",
			Active: true, CreatedAt: time.Now().UTC(),
		}
		if _, err := store.CreateResource(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListActiveResources(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"atrium", "Boardroom", "breakout", "Zebra Room"}
	if len(got) != len(want) {
		t.Fatalf("expected %d resources, got %d", len(want), len(got))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d: expected %q, got %q", i, name, got[i].Name)
		}
	}
}

func TestListActiveResourcesExcludesInactive(t *testing.T) {
	ctx := context.Background()
	store := NewResourceStore()

	active := domain.Resource{ID: uuid.New(), Name: "Alpha", Active: true, CreatedAt: time.Now().UTC()}
	closed := domain.Resource{ID: uuid.New(), Name: "Beta", Active: false, CreatedAt: time.Now().UTC()}
	for _, r := range []domain.Resource{closed, active} {
		if _, err := store.CreateResource(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListActiveResources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Alpha" {
		t.Fatalf("expected only Alpha, got %+v", got)
	}
}

func TestListActiveResourcesBreaksTiesStably(t *testing.T) {
	ctx := context.Background()
	store := NewResourceStore()

	// Same name apart from case, so the case-insensitive key ties and the secondary key
	// decides. The point is that the answer is deterministic rather than insertion order.
	for _, name := range []string{"Room B", "room b"} {
		r := domain.Resource{ID: uuid.New(), Name: name, Active: true, CreatedAt: time.Now().UTC()}
		if _, err := store.CreateResource(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListActiveResources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Name != "Room B" || got[1].Name != "room b" {
		t.Fatalf("expected the uppercase form first, got %q then %q", got[0].Name, got[1].Name)
	}
}
