// Package files is a small directory of files the admin uploads and hands out links to.
//
// Separate from the projects metadata and from game builds on purpose. A project's archive belongs to
// the project — deleting the card deletes the archive — whereas a file here belongs to nobody and
// outlives whatever page happens to link to it. Putting them in one directory would mean either a
// project delete could remove a file somebody had already shared, or a file delete could break a
// project's download.
//
// There is no metadata: the directory is the list. Name, size and modification time come from the
// filesystem, so there is nothing that can be out of step with what is actually on disk.
package files

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

// MaxFileBytes is the ceiling for one upload. Generous for a press kit or a build, finite because
// the alternative is a full disk.
const MaxFileBytes = 150 << 20

// File is one entry in the directory.
type File struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

// Store is the directory of files.
type Store struct {
	dir string
}

// NewStore creates the directory if it is missing.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("files: a directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is the directory, exposed so the caller can log where it is writing.
func (s *Store) Dir() string { return s.dir }

// List returns every file, by name.
//
// By name rather than by date: this is a place to find a thing you know you uploaded, and a list that
// reorders itself when something new arrives makes that harder.
func (s *Store) List(_ context.Context, actor domain.User) ([]File, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, apierror.Internal("could not read the files directory")
	}

	out := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !ValidName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, File{
			Name:       entry.Name(),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Save stores a file and returns what it was called.
//
// An existing name is not overwritten. Silently replacing a file somebody has already shared a link
// to is the kind of thing noticed only by the people who were relying on it, so a second upload of the
// same name becomes `name-2.ext` and the dashboard shows the result.
func (s *Store) Save(_ context.Context, actor domain.User, upload io.Reader, name string) (File, error) {
	if err := requireAdmin(actor); err != nil {
		return File{}, err
	}

	clean, err := SafeName(name)
	if err != nil {
		return File{}, err
	}

	temp, err := os.CreateTemp(s.dir, ".upload-*.tmp")
	if err != nil {
		return File{}, apierror.Internal("Could not create a place to store the file")
	}
	tempName := temp.Name()
	cleanup := func() {
		temp.Close()
		os.Remove(tempName)
	}

	// Copied with a ceiling rather than io.Copy, so a body that lies about its length cannot fill the
	// disk.
	written, err := io.Copy(temp, io.LimitReader(upload, MaxFileBytes+1))
	if err != nil {
		cleanup()
		return File{}, apierror.Internal("Could not store the file")
	}
	if written > MaxFileBytes {
		cleanup()
		return File{}, apierror.InvalidInput("That file is larger than this server accepts")
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return File{}, apierror.Internal("Could not store the file")
	}

	final := s.uniquePath(clean)
	if err := os.Rename(tempName, final); err != nil {
		os.Remove(tempName)
		return File{}, apierror.Internal("Could not install the file")
	}

	info, err := os.Stat(final)
	if err != nil {
		return File{}, apierror.Internal("Could not read back the stored file")
	}
	return File{
		Name:       filepath.Base(final),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC(),
	}, nil
}

// Delete removes a file.
func (s *Store) Delete(_ context.Context, actor domain.User, name string) (File, error) {
	if err := requireAdmin(actor); err != nil {
		return File{}, err
	}
	if !ValidName(name) {
		return File{}, apierror.NotFound("No such file")
	}

	path := filepath.Join(s.dir, name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return File{}, apierror.NotFound("No such file")
	}
	if err := os.Remove(path); err != nil {
		return File{}, apierror.Internal("Could not remove the file")
	}
	return File{Name: name, Size: info.Size(), ModifiedAt: info.ModTime().UTC()}, nil
}

// uniquePath finds a free name, appending -2, -3 and so on before the extension.
func (s *Store) uniquePath(name string) string {
	path := filepath.Join(s.dir, name)
	if _, err := os.Stat(path); err != nil {
		return path
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for n := 2; n < 1000; n++ {
		candidate := filepath.Join(s.dir, stem+"-"+strconv.Itoa(n)+extension)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return filepath.Join(s.dir, stem+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)+extension)
}

// namePattern is what a stored name may look like. Deliberately narrow: these names appear in URLs and
// are joined onto a directory, so anything outside this set is refused rather than escaped.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$`)

// ValidName reports whether a name is one this store could have written.
func ValidName(name string) bool {
	return namePattern.MatchString(name) && !strings.Contains(name, "..")
}

// SafeName turns an uploaded filename into one this store will keep.
//
// Only the base name survives: a browser may send a full path, and on Windows so may the upload. What
// is left is reduced to letters, digits, dots, dashes and underscores, because a name that has to be
// escaped to appear in a URL is a name that will be pasted wrong.
func SafeName(raw string) (string, error) {
	// Whatever separator the client used, take the last component.
	name := raw
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", apierror.InvalidInput("A file name is required")
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	cleaned := strings.TrimLeft(b.String(), ".-_")
	cleaned = strings.TrimRight(cleaned, " .")
	// Collapse runs of dashes so `a  b.txt` does not become `a--b.txt`, and drop the dash a stray space
	// leaves before the extension, because `my file .txt` reading as `my-file-.txt` is just noise.
	for strings.Contains(cleaned, "--") {
		cleaned = strings.ReplaceAll(cleaned, "--", "-")
	}
	cleaned = strings.ReplaceAll(cleaned, "-.", ".")
	cleaned = strings.TrimRight(cleaned, "-")
	if cleaned == "" {
		return "", apierror.InvalidInput("That name has nothing usable in it")
	}
	if len(cleaned) > 120 {
		extension := filepath.Ext(cleaned)
		if len(extension) > 20 {
			extension = ""
		}
		cleaned = cleaned[:120-len(extension)] + extension
	}
	if !ValidName(cleaned) {
		return "", apierror.InvalidInput("That name is not usable; letters, digits, dots, dashes and underscores only")
	}
	return cleaned, nil
}

func requireAdmin(actor domain.User) error {
	if actor.Role != domain.RoleAdmin {
		return apierror.Forbidden("Only administrators can manage files")
	}
	return nil
}
