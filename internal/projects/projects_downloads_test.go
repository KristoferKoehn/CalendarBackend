package projects

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"kristoferkoehn/CalendarBackend/internal/apierror"
)

// The downloads inventory: archives seen as files rather than as a property of a card.

func TestListDownloadsShowsTheOwner(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	raw, size := buildZip(t, map[string]string{"x.txt": "x"})
	commit, cleanup, err := s.StageArchive("hexard", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, admin(), CreateInput{
		Name: "Hexard", Description: "d", Delivery: DeliveryDownload, HasGame: true,
	}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListDownloads(ctx, admin())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d downloads", len(list))
	}
	got := list[0]
	if got.Slug != "hexard" || got.ProjectSlug != "hexard" || got.ProjectName != "Hexard" {
		t.Errorf("download = %+v", got)
	}
	if got.Href != "/downloads/hexard.zip" {
		t.Errorf("href = %q", got.Href)
	}
	if got.Size == 0 || got.ModifiedAt.IsZero() {
		t.Errorf("size or time missing: %+v", got)
	}
}

func TestListDownloadsIncludesAnOrphan(t *testing.T) {
	s := newStore(t)
	// An archive with no project is what a card deleted out of step leaves behind. The inventory must
	// still show it, or it is a file taking up space with nothing to point at it.
	raw, size := buildZip(t, map[string]string{"x.txt": "x"})
	commit, cleanup, err := s.StageArchive("stray", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := commit(); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListDownloads(context.Background(), admin())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ProjectSlug != "" || list[0].ProjectName != "" {
		t.Errorf("list = %+v", list)
	}
}

func TestListDownloadsIgnoresWhatIsNotAnArchive(t *testing.T) {
	s := newStore(t)
	// Temporary files from abandoned uploads, and anything not named like an address, are not download
	// objects and must not appear as them.
	for _, name := range []string{".upload-x-1.tmp", "notes.txt", "Hexard.zip", "no-extension"} {
		if err := os.WriteFile(filepath.Join(s.ArchivesDir(), name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListDownloads(context.Background(), admin())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("listed non-archives: %+v", list)
	}
}

func TestListDownloadsRequiresAdmin(t *testing.T) {
	s := newStore(t)
	if _, err := s.ListDownloads(context.Background(), member()); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("a member could list downloads: %v", err)
	}
}

func TestDeleteDownloadRefusesAnOwnedArchive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	raw, size := buildZip(t, map[string]string{"x.txt": "x"})
	commit, cleanup, err := s.StageArchive("owned", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, admin(), CreateInput{
		Name: "Owned", Description: "d", Delivery: DeliveryDownload, HasGame: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Removing it here would leave a card offering a file that is not there — a link that looks right
	// and 404s.
	if _, err := s.DeleteDownload(ctx, admin(), "owned"); !apierror.IsCode(err, apierror.CodeConflict) {
		t.Fatalf("an owned archive was deleted: %v", err)
	}
	if _, err := os.Stat(s.ArchivePath("owned")); err != nil {
		t.Error("the archive was removed anyway")
	}
}

func TestDeleteDownloadRemovesAnOrphan(t *testing.T) {
	s := newStore(t)
	raw, size := buildZip(t, map[string]string{"x.txt": "x"})
	commit, cleanup, err := s.StageArchive("stray", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := commit(); err != nil {
		t.Fatal(err)
	}

	removed, err := s.DeleteDownload(context.Background(), admin(), "stray")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Slug != "stray" {
		t.Errorf("removed = %+v", removed)
	}
	if _, err := os.Stat(s.ArchivePath("stray")); !os.IsNotExist(err) {
		t.Error("the archive survived")
	}
}

func TestDeleteDownloadRefusesAPath(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"../projects/hexard", "..", "a/b"} {
		if _, err := s.DeleteDownload(context.Background(), admin(), bad); !apierror.IsCode(err, apierror.CodeNotFound) {
			t.Errorf("DeleteDownload(%q) = %v", bad, err)
		}
	}
}
