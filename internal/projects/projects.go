// Package projects stores the portfolio entries and the game exports they point at.
//
// This is the one part of the service that is not backed by Postgres. The data is a directory of
// JSON documents plus a directory of extracted game builds, both outside the repository: they are
// written while the server is running, and a deploy is a `git pull`, so putting them inside the
// checkout would mean either committing uploads or losing them.
//
// One file per project rather than one index file, because the writes are independent — two admins
// creating two projects at the same time must not overwrite each other, and a single index document
// would make that the default.
package projects

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

// Status is what a project card says about itself.
type Status string

const (
	StatusLive     Status = "live"
	StatusSoon     Status = "soon"
	StatusArchived Status = "archived"
)

// Project is one card on the landing page.
type Project struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      Status `json:"status"`
	// Stack and When are display strings: the card lists the first joined by a middot and shows
	// the second at the right edge. Neither is used for anything else, so neither is constrained.
	Stack []string `json:"stack"`
	When  string   `json:"when"`
	// Href is where the card links. Defaults to the served game build, but is set explicitly for a
	// project that lives elsewhere — the calendar is at /calendar and is not a game at all.
	Href      string    `json:"href"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// UpdateInput is the editable part of a project. Every field is optional so a caller can change one
// thing without restating the rest.
type UpdateInput struct {
	Name        *string
	Description *string
	Status      *Status
	Stack       []string
	StackSet    bool
	When        *string
	Href        *string
}

// CreateInput is a new project, with the game build already staged by the caller.
type CreateInput struct {
	Name        string
	Slug        string
	Description string
	Status      Status
	Stack       []string
	When        string
	Href        string
	// HasGame says whether a build was uploaded. When it was not, the project is metadata only and
	// its Href must already point somewhere.
	HasGame bool
}

// Limits on what will be accepted. A Godot web export is tens of megabytes — the one in this
// repository is 37 — so the ceiling is generous but finite, because the alternative is a full disk.
const (
	MaxUploadBytes    = 150 << 20
	MaxExtractedBytes = 400 << 20
	MaxArchiveEntries = 2000
)

// Store reads and writes projects and the builds they point at.
type Store struct {
	// dir holds one JSON document per project.
	dir string
	// gamesDir holds one extracted build per project, named by slug.
	gamesDir string
	now      func() time.Time
}

// NewStore creates a store. Both directories are created if missing, so a fresh deployment works
// without a setup step.
func NewStore(dir, gamesDir string) (*Store, error) {
	if dir == "" || gamesDir == "" {
		return nil, errors.New("projects: a directory is required for metadata and for builds")
	}
	for _, d := range []string{dir, gamesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, gamesDir: gamesDir, now: time.Now}, nil
}

// Dir is the metadata directory, exposed so the caller can log where it is writing.
func (s *Store) Dir() string { return s.dir }

// GamesDir is the build directory.
func (s *Store) GamesDir() string { return s.gamesDir }

// List returns every project, newest first.
//
// Sorted by when it was added rather than when it was last edited: editing a description should not
// reorder somebody's portfolio.
func (s *Store) List(_ context.Context) ([]Project, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, apierror.Internal("could not read the projects directory")
	}

	out := make([]Project, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		project, err := s.read(entry.Name())
		if err != nil {
			// One unreadable document must not hide the rest. It is skipped and the others are
			// still served, which is the difference between a broken card and a broken page.
			continue
		}
		out = append(out, project)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].Name < out[j].Name
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Get returns one project.
func (s *Store) Get(_ context.Context, slug string) (Project, error) {
	if !ValidSlug(slug) {
		return Project{}, apierror.NotFound("No such project")
	}
	project, err := s.read(slug + ".json")
	if err != nil {
		return Project{}, apierror.NotFound("No such project")
	}
	return project, nil
}

// Create adds a project. The caller must have staged any build already; `CommitBuild` installs it.
func (s *Store) Create(_ context.Context, actor domain.User, in CreateInput) (Project, error) {
	if err := requireAdmin(actor); err != nil {
		return Project{}, err
	}

	slug := in.Slug
	if slug == "" {
		slug = Slugify(in.Name)
	}
	if err := ValidateSlug(slug); err != nil {
		return Project{}, err
	}
	if _, err := os.Stat(s.path(slug)); err == nil {
		return Project{}, apierror.Conflict("A project with that address already exists")
	}

	status := in.Status
	if status == "" {
		status = StatusLive
	}
	href := in.Href
	if href == "" {
		// A build that was uploaded is served by the games route; anything else has to say where
		// it lives, because there is nothing to infer.
		if !in.HasGame {
			return Project{}, apierror.InvalidInput("An address is required when no game build is uploaded")
		}
		href = GameHref(slug)
	}
	if err := ValidateHref(href); err != nil {
		return Project{}, err
	}
	if err := ValidateText(in.Name, in.Description, status, in.Stack, in.When); err != nil {
		return Project{}, err
	}

	now := s.now().UTC()
	project := Project{
		Slug:        slug,
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		Status:      status,
		Stack:       cleanStack(in.Stack),
		When:        strings.TrimSpace(in.When),
		Href:        href,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.write(project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// Update changes a project's details. The build is untouched: replacing one is a separate upload,
// so a description can be fixed without risking the files it points at.
func (s *Store) Update(_ context.Context, actor domain.User, slug string, in UpdateInput) (Project, error) {
	if err := requireAdmin(actor); err != nil {
		return Project{}, err
	}
	if !ValidSlug(slug) {
		return Project{}, apierror.NotFound("No such project")
	}
	project, err := s.read(slug + ".json")
	if err != nil {
		return Project{}, apierror.NotFound("No such project")
	}

	if in.Name != nil {
		project.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		project.Description = strings.TrimSpace(*in.Description)
	}
	if in.Status != nil {
		project.Status = *in.Status
	}
	if in.StackSet {
		project.Stack = cleanStack(in.Stack)
	}
	if in.When != nil {
		project.When = strings.TrimSpace(*in.When)
	}
	if in.Href != nil {
		if err := ValidateHref(*in.Href); err != nil {
			return Project{}, err
		}
		project.Href = strings.TrimSpace(*in.Href)
	}
	if err := ValidateText(project.Name, project.Description, project.Status, project.Stack, project.When); err != nil {
		return Project{}, err
	}

	project.UpdatedAt = s.now().UTC()
	if err := s.write(project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// Delete removes a project and the build it points at, if that build is one of ours.
//
// A project whose Href is somewhere else — the calendar — loses its card and nothing else. Deleting
// the calendar because its description was removed would be a surprising interpretation of
// "remove this project".
func (s *Store) Delete(_ context.Context, actor domain.User, slug string) (Project, error) {
	if err := requireAdmin(actor); err != nil {
		return Project{}, err
	}
	if !ValidSlug(slug) {
		return Project{}, apierror.NotFound("No such project")
	}
	project, err := s.read(slug + ".json")
	if err != nil {
		return Project{}, apierror.NotFound("No such project")
	}

	if err := os.Remove(s.path(slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Project{}, apierror.Internal("could not remove the project")
	}
	if buildDir, ok := s.buildDirFor(slug, project.Href); ok {
		if err := os.RemoveAll(buildDir); err != nil {
			return Project{}, apierror.Internal("could not remove the game files")
		}
	}
	return project, nil
}

// GameHref is where an uploaded build is served from.
func GameHref(slug string) string { return "/games/" + slug + "/" }

// ValidSlug reports whether a slug is one this store could have written.
func ValidSlug(slug string) bool { return slugPattern.MatchString(slug) }

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// Slugify derives an address from a title. It is a convenience for the form, not a guarantee —
// anything it cannot reduce to an address has to be supplied by the caller.
func Slugify(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ValidateSlug reports why an address is unusable, in words a form can show.
func ValidateSlug(slug string) error {
	if slug == "" {
		return apierror.InvalidInput("An address is required")
	}
	if !ValidSlug(slug) {
		return apierror.InvalidInput("The address must be lower-case letters, digits and hyphens, up to 60 characters")
	}
	return nil
}

// ValidateHref keeps a card's link inside this site.
//
// An absolute URL is refused deliberately: a card is part of this page, and a link that leaves it
// is a different feature with different questions — who is responsible for the destination, and
// what happens when it moves.
func ValidateHref(href string) error {
	href = strings.TrimSpace(href)
	if href == "" {
		return apierror.InvalidInput("An address is required")
	}
	if !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") {
		return apierror.InvalidInput("The address must start with a single slash")
	}
	if strings.Contains(href, "..") {
		return apierror.InvalidInput("The address cannot contain '..'")
	}
	return nil
}

// ValidateText checks the fields a card actually displays.
func ValidateText(name, description string, status Status, stack []string, when string) error {
	if strings.TrimSpace(name) == "" {
		return apierror.InvalidInput("A title is required")
	}
	if len([]rune(name)) > 120 {
		return apierror.InvalidInput("The title is too long")
	}
	if strings.TrimSpace(description) == "" {
		return apierror.InvalidInput("A description is required")
	}
	if len([]rune(description)) > 2000 {
		return apierror.InvalidInput("The description is too long")
	}
	switch status {
	case StatusLive, StatusSoon, StatusArchived:
	default:
		return apierror.InvalidInput("Status must be live, soon or archived")
	}
	if len([]rune(when)) > 40 {
		return apierror.InvalidInput("The date is too long")
	}
	if len(stack) > 12 {
		return apierror.InvalidInput("Too many stack entries")
	}
	for _, entry := range stack {
		if len([]rune(entry)) > 40 {
			return apierror.InvalidInput("A stack entry is too long")
		}
	}
	return nil
}

func cleanStack(stack []string) []string {
	out := make([]string, 0, len(stack))
	for _, entry := range stack {
		trimmed := strings.TrimSpace(entry)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func requireAdmin(actor domain.User) error {
	if actor.Role != domain.RoleAdmin {
		return apierror.Forbidden("Only administrators can manage projects")
	}
	return nil
}

// path is the metadata file for a slug. Callers validate the slug first.
func (s *Store) path(slug string) string {
	return filepath.Join(s.dir, slug+".json")
}

// buildDirFor reports the build directory for a project, but only when the project's own address
// points at that build. A project hosted elsewhere has no local files to remove.
func (s *Store) buildDirFor(slug, href string) (string, bool) {
	if !strings.HasPrefix(href, "/games/") {
		return "", false
	}
	return filepath.Join(s.gamesDir, slug), true
}

func (s *Store) read(name string) (Project, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return Project{}, err
	}
	var project Project
	if err := json.Unmarshal(raw, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// write replaces a project document atomically.
//
// The temporary file and the rename matter: a half-written JSON document is a card that fails to
// parse, and the reader skips those, so the project would silently vanish rather than appear.
func (s *Store) write(project Project) error {
	body, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return apierror.Internal("could not encode the project")
	}
	final := s.path(project.Slug)
	temp, err := os.CreateTemp(s.dir, project.Slug+".*.tmp")
	if err != nil {
		return apierror.Internal("could not write the project")
	}
	tempName := temp.Name()
	defer func() {
		temp.Close()
		os.Remove(tempName)
	}()

	if _, err := temp.Write(append(body, '\n')); err != nil {
		return apierror.Internal("could not write the project")
	}
	if err := temp.Sync(); err != nil {
		return apierror.Internal("could not write the project")
	}
	if err := temp.Close(); err != nil {
		return apierror.Internal("could not write the project")
	}
	if err := os.Rename(tempName, final); err != nil {
		return apierror.Internal("could not write the project")
	}
	return nil
}

// StageBuild extracts an uploaded archive into a temporary directory next to the builds and returns
// a function that installs it.
//
// Extracting before the metadata is written is what makes an upload safe to fail: until
// `CommitBuild` runs, an interrupted or invalid upload has changed nothing that anything reads, and
// the project card does not exist yet.
func (s *Store) StageBuild(slug string, archive io.ReaderAt, size int64) (func() error, func(), error) {
	tempDir, err := os.MkdirTemp(s.gamesDir, ".upload-"+slug+"-")
	if err != nil {
		return nil, nil, apierror.Internal("Could not create a place to unpack the game")
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	if err := extract(archive, size, tempDir); err != nil {
		cleanup()
		return nil, nil, err
	}

	final := filepath.Join(s.gamesDir, slug)
	commit := func() error {
		// Replace rather than merge: a rebuild that drops a file must not leave the old one behind
		// for the browser to keep asking for.
		if err := os.RemoveAll(final); err != nil {
			return apierror.Internal("Could not replace the existing game")
		}
		if err := os.Rename(tempDir, final); err != nil {
			return apierror.Internal("Could not install the game")
		}
		return nil
	}
	return commit, cleanup, nil
}

// extract unpacks a zip into dir, refusing anything that would land outside it.
func extract(archive io.ReaderAt, size int64, dir string) error {
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return apierror.InvalidInput("That file is not a readable zip archive")
	}
	if len(reader.File) > MaxArchiveEntries {
		return apierror.InvalidInput("That archive has too many files")
	}

	var written int64
	for _, entry := range reader.File {
		// A zip entry name is attacker-controlled. Cleaning it and then refusing anything that is
		// still absolute or still climbs out is what stops a "zip slip" — an entry named
		// "../../server.exe" writing outside the directory it was meant for.
		name := path.Clean(strings.ReplaceAll(entry.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || name == ".." {
			return apierror.InvalidInput("That archive contains an unsafe path")
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		relative, err := filepath.Rel(dir, target)
		if err != nil || strings.HasPrefix(relative, "..") {
			return apierror.InvalidInput("That archive contains an unsafe path")
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return apierror.Internal("Could not unpack the game")
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return apierror.Internal("Could not unpack the game")
		}
		body, err := entry.Open()
		if err != nil {
			return apierror.InvalidInput("That archive could not be read")
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			body.Close()
			return apierror.Internal("Could not unpack the game")
		}

		// Copied with a ceiling rather than io.Copy, so a decompression bomb cannot fill the disk.
		remaining := MaxExtractedBytes - written
		copied, err := io.Copy(file, io.LimitReader(body, remaining+1))
		body.Close()
		file.Close()
		if err != nil {
			return apierror.Internal("Could not unpack the game")
		}
		written += copied
		if written > MaxExtractedBytes {
			return apierror.InvalidInput("That archive unpacks to more than this server will store")
		}
	}

	// What makes it a Godot web export rather than any zip: the entry document and the engine.
	for _, required := range []string{"index.html", "index.wasm"} {
		if _, err := os.Stat(filepath.Join(dir, required)); err != nil {
			return apierror.InvalidInput("That archive is missing " + required + " — export the game for the web first")
		}
	}
	return nil
}
