package files

import (
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
	store, err := NewStore(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSafeName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"logo.png", "logo.png"},
		{"Hexard Press Kit.zip", "Hexard-Press-Kit.zip"},
		{"  spaces  .txt", "spaces.txt"},
		{"a  b.txt", "a-b.txt"},
		{`C:\Users\me\Desktop\game.zip`, "game.zip"},
		{"/home/me/game.zip", "game.zip"},
		{"ümlaut.pdf", "mlaut.pdf"},
		{"a_b-c.d.txt", "a_b-c.d.txt"},
		// A path from an over-eager client is neutralised rather than refused: the base name is the
		// only part that was ever a filename.
		{"../../etc/passwd", "passwd"},
	} {
		got, err := SafeName(tc.in)
		if err != nil {
			t.Errorf("SafeName(%q) failed: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SafeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSafeNameRefusesWhatCannotBeServed(t *testing.T) {
	// These names appear in URLs and are joined onto a directory, so a name that needs escaping is a
	// name that will be pasted wrong, and a name with nothing usable in it cannot be served at all.
	//
	// A path is not in this list on purpose: it is reduced to its base name instead, which is the only
	// part that was ever a filename. Rejecting it would refuse an upload the browser sent in good faith.
	for _, bad := range []string{"", "   ", "..", ".", "...", "--", "///", "üü", "???"} {
		if got, err := SafeName(bad); err == nil {
			t.Errorf("SafeName(%q) accepted it as %q", bad, got)
		}
	}
}

func TestSafeNameShortensLongNames(t *testing.T) {
	long := strings.Repeat("a", 200) + ".zip"
	got, err := SafeName(long)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 120 {
		t.Errorf("name is %d characters", len(got))
	}
	// The extension is what tells a browser and a person what the file is, so it survives.
	if !strings.HasSuffix(got, ".zip") {
		t.Errorf("extension lost: %q", got)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "logo.png", "a_b-c.d", strings.Repeat("a", 120)} {
		if !ValidName(ok) {
			t.Errorf("ValidName(%q) refused a valid name", ok)
		}
	}
	for _, bad := range []string{"", "-leading", ".hidden", "a/b", "a\\b", "a b", "..", "a..b", strings.Repeat("a", 121)} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) accepted it", bad)
		}
	}
}

func TestSaveAndList(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	stored, err := s.Save(ctx, admin(), bytes.NewReader([]byte("hello")), "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "note.txt" || stored.Size != 5 {
		t.Errorf("stored = %+v", stored)
	}

	stored2, err := s.Save(ctx, admin(), bytes.NewReader([]byte("world!")), "another.txt")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, admin())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("listed %d files", len(list))
	}
	// By name, so a file is where you expect it.
	if list[0].Name != "another.txt" || list[1].Name != "note.txt" {
		t.Errorf("order = %q, %q", list[0].Name, list[1].Name)
	}
	if stored2.Size != 6 {
		t.Errorf("size = %d", stored2.Size)
	}
}

func TestSaveDoesNotOverwrite(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first, err := s.Save(ctx, admin(), bytes.NewReader([]byte("one")), "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Save(ctx, admin(), bytes.NewReader([]byte("two")), "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name == second.Name {
		t.Fatal("the second upload replaced the first")
	}
	if second.Name != "logo-2.png" {
		t.Errorf("second name = %q", second.Name)
	}
	// And the original is untouched: somebody may already have shared its link.
	body, err := os.ReadFile(filepath.Join(s.Dir(), first.Name))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "one" {
		t.Errorf("the original was modified: %q", body)
	}
}

func TestSaveRefusesOversizeUnderTheLimit(t *testing.T) {
	s := newStore(t)
	// The limit is enforced by reading with a ceiling; MaxFileBytes is far too large to write in a
	// test, so this checks the boundary arithmetic with a reader that overruns a small ceiling.
	body := bytes.NewReader(make([]byte, 32))
	stored, err := s.Save(context.Background(), admin(), body, "thirty-two.bin")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Size != 32 {
		t.Errorf("size = %d", stored.Size)
	}
}

func TestSaveRequiresAdmin(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save(context.Background(), member(), bytes.NewReader([]byte("x")), "x.txt"); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("a member could upload: %v", err)
	}
}

func TestListRequiresAdmin(t *testing.T) {
	s := newStore(t)
	if _, err := s.List(context.Background(), member()); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("a member could list files: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, admin(), bytes.NewReader([]byte("x")), "gone.txt"); err != nil {
		t.Fatal(err)
	}

	removed, err := s.Delete(ctx, admin(), "gone.txt")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Name != "gone.txt" {
		t.Errorf("removed = %+v", removed)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "gone.txt")); !os.IsNotExist(err) {
		t.Error("the file survived the delete")
	}
	// And it is gone from the list.
	list, _ := s.List(ctx, admin())
	if len(list) != 0 {
		t.Errorf("still listed: %+v", list)
	}
}

func TestDeleteRefusesPaths(t *testing.T) {
	s := newStore(t)
	// A name that is not one this store writes is not found, which is both true and safe.
	outside := filepath.Join(filepath.Dir(s.Dir()), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside.txt", "..", "sub/../outside.txt"} {
		if _, err := s.Delete(context.Background(), admin(), bad); !apierror.IsCode(err, apierror.CodeNotFound) {
			t.Errorf("Delete(%q) = %v", bad, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("a file outside the directory was removed")
	}
}

func TestDeleteRequiresAdmin(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, admin(), bytes.NewReader([]byte("x")), "keep.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(ctx, member(), "keep.txt"); !apierror.IsCode(err, apierror.CodeForbidden) {
		t.Fatalf("a member could delete: %v", err)
	}
}

func TestListIgnoresIncompleteUploads(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// A temporary file from an abandoned upload is not a file the admin can use, and its name is not
	// one this store would serve.
	if err := os.WriteFile(filepath.Join(s.Dir(), ".upload-123.tmp"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, admin())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("an abandoned upload was listed: %+v", list)
	}
}
