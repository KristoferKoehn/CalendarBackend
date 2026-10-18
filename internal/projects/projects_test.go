package projects

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func admin() domain.User  { return domain.User{Role: domain.RoleAdmin} }
func member() domain.User { return domain.User{Role: domain.RoleUser} }
func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "projects"), filepath.Join(t.TempDir(), "games"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSlugify(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Match 3", "match-3"},
		{"CalendarSite", "calendarsite"},
		{"  Trailing   spaces  ", "trailing-spaces"},
		{"Symbols!@#$%", "symbols"},
		{"Already-hyphenated", "already-hyphenated"},
		{"MiXeD CaSe", "mixed-case"},
		{"", ""},
	} {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"a", "match-3", "a1-b2", strings.Repeat("a", 60)} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("ValidateSlug(%q) refused a valid slug: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-leading", "UPPER", "has space", "has_underscore", "has/slash", "..", strings.Repeat("a", 61)} {
		if err := ValidateSlug(bad); err == nil {
			t.Errorf("ValidateSlug(%q) accepted it", bad)
		}
	}
}

func TestValidateHref(t *testing.T) {
	for _, ok := range []string{"/calendar", "/games/match-3/", "/"} {
		if err := ValidateHref(ok); err != nil {
			t.Errorf("ValidateHref(%q) refused a valid address: %v", ok, err)
		}
	}
	// Absolute URLs are refused on purpose: a card is part of this page. `//host` is refused
	// because it is a protocol-relative absolute URL wearing a leading slash.
	for _, bad := range []string{"", "games/x", "https://example.com", "//example.com", "/a/../b"} {
		if err := ValidateHref(bad); err == nil {
			t.Errorf("ValidateHref(%q) accepted it", bad)
		}
	}
}

func TestCreateRequiresAdmin(t *testing.T) {
	s := newStore(t)
	_, err := s.Create(context.Background(), member(), CreateInput{
		Name: "Game", Description: "d", Href: "/games/game/",
	})
	if !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("a member could create a project: %v", err)
	}
}

func TestCreateAndRead(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, admin(), CreateInput{
		Name: "Match 3", Description: "A puzzle game.", Status: StatusLive,
		Stack: []string{"Godot", " ", "GDScript"}, When: "2026", Href: "/games/match-3/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Slug != "match-3" {
		t.Errorf("slug = %q", created.Slug)
	}
	// Blank stack entries are dropped rather than shown as empty separators.
	if len(created.Stack) != 2 || created.Stack[0] != "Godot" || created.Stack[1] != "GDScript" {
		t.Errorf("stack = %#v", created.Stack)
	}

	got, err := s.Get(ctx, "match-3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Match 3" || got.Href != "/games/match-3/" {
		t.Errorf("round trip lost data: %#v", got)
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %d, %v", len(list), err)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	in := CreateInput{Name: "Same", Description: "d", Href: "/games/same/"}
	if _, err := s.Create(ctx, admin(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, admin(), in); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("a duplicate address was accepted: %v", err)
	}
}

func TestCreateWithoutBuildNeedsAnAddress(t *testing.T) {
	s := newStore(t)
	// The calendar is a project with no uploaded build; without an Href there is nothing to point
	// the card at, so it is refused rather than guessed.
	_, err := s.Create(context.Background(), admin(), CreateInput{Name: "Calendar", Description: "d"})
	if !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("an address-less project was accepted: %v", err)
	}
	// With one, it is fine.
	if _, err := s.Create(context.Background(), admin(), CreateInput{
		Name: "Calendar", Description: "d", Href: "/calendar",
	}); err != nil {
		t.Fatalf("a project hosted elsewhere was refused: %v", err)
	}
}

func TestUpdateChangesOnlyWhatIsGiven(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, admin(), CreateInput{
		Name: "Orig", Description: "original", Stack: []string{"Go"}, When: "2025", Href: "/games/orig/",
	}); err != nil {
		t.Fatal(err)
	}

	newName := "Renamed"
	updated, err := s.Update(ctx, admin(), "orig", UpdateInput{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("name = %q", updated.Name)
	}
	if updated.Description != "original" || updated.When != "2025" || len(updated.Stack) != 1 {
		t.Errorf("an unrelated field was changed: %#v", updated)
	}
}

func TestUpdateRequiresAdmin(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, admin(), CreateInput{Name: "X", Description: "d", Href: "/games/x/"}); err != nil {
		t.Fatal(err)
	}
	name := "hacked"
	if _, err := s.Update(ctx, member(), "x", UpdateInput{Name: &name}); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("a member could update a project: %v", err)
	}
}

func TestDeleteRemovesTheBuildOnlyWhenItIsOurs(t *testing.T) {
	ctx := context.Background()

	t.Run("a games project takes its files with it", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Create(ctx, admin(), CreateInput{Name: "G", Description: "d", Href: "/games/g/"}); err != nil {
			t.Fatal(err)
		}
		build := filepath.Join(s.GamesDir(), "g")
		if err := os.MkdirAll(build, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Delete(ctx, admin(), "g"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(build); !os.IsNotExist(err) {
			t.Error("the build directory survived the delete")
		}
	})

	t.Run("a project hosted elsewhere keeps its files", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Create(ctx, admin(), CreateInput{Name: "C", Description: "d", Href: "/calendar"}); err != nil {
			t.Fatal(err)
		}
		// A same-named directory must not be touched, because the card's address says the project
		// lives somewhere this store does not own.
		stray := filepath.Join(s.GamesDir(), "c")
		if err := os.MkdirAll(stray, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Delete(ctx, admin(), "c"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(stray); err != nil {
			t.Error("deleting a card removed files it did not own")
		}
	})
}

// buildZip makes an archive from name -> contents.
func buildZip(t *testing.T, files map[string]string) ([]byte, int64) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buffer.Bytes()
	return raw, int64(len(raw))
}

func TestStageBuildInstallsAValidExport(t *testing.T) {
	s := newStore(t)
	raw, size := buildZip(t, map[string]string{
		"index.html": "<html></html>",
		"index.wasm": "wasm",
		"index.pck":  "pck",
	})
	commit, cleanup, err := s.StageBuild("game", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// Nothing is visible until the commit, which is what makes a failed upload harmless.
	if _, err := os.Stat(filepath.Join(s.GamesDir(), "game")); !os.IsNotExist(err) {
		t.Fatal("the build was installed before it was committed")
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "index.wasm", "index.pck"} {
		if _, err := os.Stat(filepath.Join(s.GamesDir(), "game", name)); err != nil {
			t.Errorf("%s was not installed: %v", name, err)
		}
	}
}

func TestStageBuildRejectsANonExport(t *testing.T) {
	s := newStore(t)
	raw, size := buildZip(t, map[string]string{"index.html": "<html></html>"})
	if _, _, err := s.StageBuild("game", bytes.NewReader(raw), size); !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("an archive without an engine was accepted: %v", err)
	}
	// And it leaves nothing behind.
	entries, err := os.ReadDir(s.GamesDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected upload left %d entries behind", len(entries))
	}
}

func TestStageBuildRejectsPathEscapes(t *testing.T) {
	// A zip entry name is attacker-controlled. Each of these would write outside the target
	// directory if it were joined naively.
	for _, name := range []string{
		"../escaped.txt",
		"../../escaped.txt",
		"a/../../escaped.txt",
		"/absolute.txt",
		`..\windows-escaped.txt`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			raw, size := buildZip(t, map[string]string{
				"index.html": "<html></html>",
				"index.wasm": "wasm",
				name:         "escaped",
			})
			if _, _, err := s.StageBuild("game", bytes.NewReader(raw), size); err == nil {
				t.Fatalf("an archive containing %q was accepted", name)
			}
			// Nothing may have been written next to the games directory.
			sibling := filepath.Join(filepath.Dir(s.GamesDir()), "escaped.txt")
			if _, err := os.Stat(sibling); err == nil {
				t.Fatalf("%q escaped the games directory", name)
			}
		})
	}
}

func TestListSkipsUnreadableDocuments(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, admin(), CreateInput{Name: "Good", Description: "d", Href: "/games/good/"}); err != nil {
		t.Fatal(err)
	}
	// A half-written or hand-broken file must not take the whole page down with it.
	if err := os.WriteFile(filepath.Join(s.Dir(), "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Slug != "good" {
		t.Errorf("List = %#v", list)
	}
}
