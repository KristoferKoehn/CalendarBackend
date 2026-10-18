package projects

import (
	"bytes"
	"context"
	"os"
	"testing"

	"kristoferkoehn/CalendarBackend/internal/apierror"
)

// A download project keeps the upload intact and points its card at the file.
//
// The alternative — unpacking it like a game — would mean a card offering "download" hands over a
// directory of extracted files, which is not the archive the admin made.

func TestDownloadKeepsTheArchiveAndPointsAtIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	raw, size := buildZip(t, map[string]string{"anything.txt": "whatever the admin made"})
	commit, cleanup, err := s.StageArchive("art-pack", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	project, err := s.Create(ctx, admin(), CreateInput{
		Name:        "Art pack",
		Description: "Source files.",
		Href:        "", // left to the server: a download points at its archive
		Delivery:    DeliveryDownload,
		HasGame:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}

	if project.Delivery != DeliveryDownload {
		t.Errorf("delivery = %q", project.Delivery)
	}
	if project.Href != "/downloads/art-pack.zip" {
		t.Errorf("href = %q, want the archive", project.Href)
	}

	stored, err := os.ReadFile(s.ArchivePath("art-pack"))
	if err != nil {
		t.Fatalf("the archive was not stored: %v", err)
	}
	if !bytes.Equal(stored, raw) {
		t.Error("the stored archive is not the bytes that were uploaded")
	}
	// And nothing was unpacked.
	if entries, _ := os.ReadDir(s.GamesDir()); len(entries) != 0 {
		t.Errorf("a download also created %d build entries", len(entries))
	}
}

func TestDownloadDoesNotNeedToBeAWebExport(t *testing.T) {
	s := newStore(t)
	// The export check exists for playable games; a download is whatever the admin wants to hand
	// over, so a zip with no index.html is perfectly valid here.
	raw, size := buildZip(t, map[string]string{"readme.txt": "notes"})
	if _, _, err := s.StageArchive("notes", bytes.NewReader(raw), size); err != nil {
		t.Fatalf("a plain zip was refused as a download: %v", err)
	}
}

func TestDownloadMustStillBeAZip(t *testing.T) {
	s := newStore(t)
	// A corrupted upload discovered later is a broken promise with nothing to explain it.
	if _, _, err := s.StageArchive("junk", bytes.NewReader([]byte("not a zip")), 9); !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("a non-zip was accepted as a download: %v", err)
	}
}

func TestStageArchiveReplacesAnExistingFile(t *testing.T) {
	s := newStore(t)
	first, size := buildZip(t, map[string]string{"a.txt": "one"})
	commit, cleanup, err := s.StageArchive("p", bytes.NewReader(first), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := commit(); err != nil {
		t.Fatal(err)
	}

	second, size2 := buildZip(t, map[string]string{"b.txt": "two"})
	commit2, cleanup2, err := s.StageArchive("p", bytes.NewReader(second), size2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if err := commit2(); err != nil {
		t.Fatal(err)
	}

	stored, err := os.ReadFile(s.ArchivePath("p"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, second) {
		t.Error("the replacement did not take effect")
	}
}

func TestDeletingADownloadRemovesTheArchive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	raw, size := buildZip(t, map[string]string{"x.txt": "x"})
	commit, cleanup, err := s.StageArchive("gone", bytes.NewReader(raw), size)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, admin(), CreateInput{
		Name: "Gone", Description: "d", Delivery: DeliveryDownload, HasGame: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Delete(ctx, admin(), "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.ArchivePath("gone")); !os.IsNotExist(err) {
		t.Error("the archive survived the delete")
	}
}

func TestDeliveryDefaultsToPlay(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Documents written before delivery existed, and clients that never send it, must keep working.
	project, err := s.Create(ctx, admin(), CreateInput{
		Name: "Older", Description: "d", HasGame: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.Kind() != DeliveryPlay {
		t.Errorf("kind = %q", project.Kind())
	}
	if project.Href != "/games/older/" {
		t.Errorf("href = %q", project.Href)
	}

	// And an explicitly empty field on an old document reads as play.
	var old Project
	if old.Kind() != DeliveryPlay {
		t.Error("an empty delivery is not treated as play")
	}
}

func TestDownloadRequiresAnUpload(t *testing.T) {
	s := newStore(t)
	// A download with nothing to download would be a card that leads to a 404.
	_, err := s.Create(context.Background(), admin(), CreateInput{
		Name: "Empty", Description: "d", Delivery: DeliveryDownload,
	})
	if !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("a download with no file was accepted: %v", err)
	}
}

func TestUnknownDeliveryIsRefused(t *testing.T) {
	s := newStore(t)
	_, err := s.Create(context.Background(), admin(), CreateInput{
		Name: "Odd", Description: "d", Delivery: Delivery("stream"), Href: "/games/odd/",
	})
	if !apierror.IsCode(err, apierror.CodeInvalidInput) {
		t.Fatalf("an unknown delivery was accepted: %v", err)
	}
}
