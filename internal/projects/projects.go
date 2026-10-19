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

// Delivery is what happens when somebody follows a project's card.
type Delivery string

const (
	// DeliveryPlay unpacks the uploaded archive and serves it as a game.
	DeliveryPlay Delivery = "play"
	// DeliveryDownload keeps the archive intact and hands the file over instead.
	DeliveryDownload Delivery = "download"
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
	// Delivery says whether the uploaded archive became a playable build or stayed a file. Empty
	// means play, so documents written before this existed keep working.
	Delivery Delivery `json:"delivery"`
	// Href is where the card links. Defaults to the served game build, or to the archive, but is set
	// explicitly for a project that lives elsewhere — the calendar is at /calendar and is not a game.
	Href      string    `json:"href"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Delivery reports what following this project does, defaulting to play for older documents.
func (p Project) Kind() Delivery {
	if p.Delivery == "" {
		return DeliveryPlay
	}
	return p.Delivery
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
	// Delivery is what the card does. Empty means play.
	Delivery Delivery
	// HasGame says whether a file was uploaded. When it was not, the project is metadata only and
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
	// archivesDir holds one intact upload per downloadable project, named <slug>.zip.
	archivesDir string
	now         func() time.Time
}

// NewStore creates a store. Every directory is created if missing, so a fresh deployment works
// without a setup step.
func NewStore(dir, gamesDir, archivesDir string) (*Store, error) {
	if dir == "" || gamesDir == "" || archivesDir == "" {
		return nil, errors.New("projects: a directory is required for metadata, builds and archives")
	}
	for _, d := range []string{dir, gamesDir, archivesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, gamesDir: gamesDir, archivesDir: archivesDir, now: time.Now}, nil
}

// Dir is the metadata directory, exposed so the caller can log where it is writing.
func (s *Store) Dir() string { return s.dir }

// GamesDir is the build directory.
func (s *Store) GamesDir() string { return s.gamesDir }

// ArchivesDir is where downloadable uploads are kept.
func (s *Store) ArchivesDir() string { return s.archivesDir }

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

	delivery := in.Delivery
	if delivery == "" {
		delivery = DeliveryPlay
	}
	switch delivery {
	case DeliveryPlay, DeliveryDownload:
	default:
		return Project{}, apierror.InvalidInput("Delivery must be play or download")
	}

	href := in.Href
	if href == "" {
		// What an upload becomes decides where its card points: a build is served at its own
		// address, an archive is handed over as a file. Anything with no upload at all has to say
		// where it lives, because there is nothing to infer.
		switch {
		case !in.HasGame:
			return Project{}, apierror.InvalidInput("An address is required when no file is uploaded")
		case delivery == DeliveryDownload:
			href = DownloadHref(slug)
		default:
			href = GameHref(slug)
		}
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
	if in.HasGame {
		project.Delivery = delivery
	}
	if err := s.write(project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// DownloadHref is where a downloadable upload is served from.
func DownloadHref(slug string) string { return "/downloads/" + slug + ".zip" }

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
	// What was uploaded decides what comes with it: an archive, a build directory, or nothing.
	if project.Kind() == DeliveryDownload {
		if err := os.Remove(s.archivePath(slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Project{}, apierror.Internal("could not remove the download")
		}
	} else if buildDir, ok := s.buildDirFor(slug, project.Href); ok {
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

// Download is an archive on the shelf, seen as a thing in its own right.
//
// Until now an archive existed only as a property of a project: it was created by the upload form and
// deleted with the card, and there was nowhere to look at one. This is the inventory that makes it
// visible, together with the project that owns it.
type Download struct {
	// Slug is the address the archive is stored under, which is also the project's address.
	Slug       string    `json:"slug"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	// Href is where the file is served.
	Href string `json:"href"`
	// ProjectSlug is empty for an archive no project points at — which is what a card deleted out of
	// step would leave behind, and the only kind that can be removed from here.
	ProjectSlug string `json:"projectSlug"`
	ProjectName string `json:"projectName"`
}

// ListDownloads returns every stored archive, by project name.
func (s *Store) ListDownloads(_ context.Context, actor domain.User) ([]Download, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.archivesDir)
	if err != nil {
		return nil, apierror.Internal("could not read the downloads directory")
	}

	// One pass over the projects, so each archive can be matched to its owner rather than each archive
	// re-reading every document.
	owners := map[string]Project{}
	projects, err := s.List(context.Background())
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		owners[project.Slug] = project
	}

	out := make([]Download, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		slug, ok := strings.CutSuffix(entry.Name(), ".zip")
		if !ok || !ValidSlug(slug) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		download := Download{
			Slug:       slug,
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC(),
			Href:       DownloadHref(slug),
		}
		if project, found := owners[slug]; found && project.Kind() == DeliveryDownload {
			download.ProjectSlug = project.Slug
			download.ProjectName = project.Name
		}
		out = append(out, download)
	}

	sort.Slice(out, func(i, j int) bool {
		// Named ones first: an archive with no project is the odd case and belongs at the end.
		if (out[i].ProjectName == "") != (out[j].ProjectName == "") {
			return out[i].ProjectName != ""
		}
		return out[i].ProjectName < out[j].ProjectName
	})
	return out, nil
}

// DeleteDownload removes an archive that no project points at.
//
// Refused while a project owns it, because the card would then offer a file that is not there — a
// link that looks correct and 404s. Deleting the project is how that archive goes.
func (s *Store) DeleteDownload(_ context.Context, actor domain.User, slug string) (Download, error) {
	if err := requireAdmin(actor); err != nil {
		return Download{}, err
	}
	if !ValidSlug(slug) {
		return Download{}, apierror.NotFound("No such download")
	}

	info, err := os.Stat(s.archivePath(slug))
	if err != nil || !info.Mode().IsRegular() {
		return Download{}, apierror.NotFound("No such download")
	}
	if project, err := s.read(slug + ".json"); err == nil && project.Kind() == DeliveryDownload {
		return Download{}, apierror.Conflict("That download belongs to " + project.Name + "; delete the project instead")
	}
	if err := os.Remove(s.archivePath(slug)); err != nil {
		return Download{}, apierror.Internal("Could not remove the download")
	}
	return Download{
		Slug:       slug,
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC(),
		Href:       DownloadHref(slug),
	}, nil
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

// archivePath is the stored upload for a downloadable project.
func (s *Store) archivePath(slug string) string {
	return filepath.Join(s.archivesDir, slug+".zip")
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

// StageArchive copies an upload, intact, to be installed as a download.
//
// The counterpart of StageBuild for a project whose card hands the file over instead of serving it:
// nothing is unpacked and nothing is inspected beyond being a readable zip, because the point is to
// give the visitor the file the admin made.
//
// Same staging discipline — a temporary file beside the archives, installed by a rename — so a
// failed or abandoned upload leaves no half-written file where a download link points.
func (s *Store) StageArchive(slug string, archive io.Reader, size int64) (func() error, func(), error) {
	if size > MaxUploadBytes {
		return nil, nil, apierror.InvalidInput("That file is larger than this server accepts")
	}

	temp, err := os.CreateTemp(s.archivesDir, ".upload-"+slug+"-*.tmp")
	if err != nil {
		return nil, nil, apierror.Internal("Could not create a place to store the file")
	}
	tempName := temp.Name()
	cleanup := func() {
		temp.Close()
		os.Remove(tempName)
	}

	// Copied with a ceiling rather than io.Copy, so a file that lies about its size cannot fill the
	// disk.
	written, err := io.Copy(temp, io.LimitReader(archive, MaxUploadBytes+1))
	if err != nil {
		cleanup()
		return nil, nil, apierror.Internal("Could not store the file")
	}
	if written > MaxUploadBytes {
		cleanup()
		return nil, nil, apierror.InvalidInput("That file is larger than this server accepts")
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return nil, nil, apierror.Internal("Could not store the file")
	}

	// A download is still an archive: the card promises something openable, and a corrupted upload
	// discovered later is a broken promise with nothing to explain it.
	if err := verifyZip(tempName); err != nil {
		os.Remove(tempName)
		return nil, nil, err
	}

	final := s.archivePath(slug)
	commit := func() error {
		if err := os.Remove(final); err != nil && !errors.Is(err, os.ErrNotExist) {
			return apierror.Internal("Could not replace the existing file")
		}
		if err := os.Rename(tempName, final); err != nil {
			return apierror.Internal("Could not install the file")
		}
		return nil
	}
	return commit, cleanup, nil
}

// verifyZip reports whether a file on disk is a readable zip archive.
func verifyZip(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return apierror.InvalidInput("That file is not a readable zip archive")
	}
	return reader.Close()
}

// ArchivePath is the stored upload for a downloadable project. Exported so the route that serves
// downloads can resolve it without knowing the layout.
func (s *Store) ArchivePath(slug string) string { return s.archivePath(slug) }

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

	// The export is not necessarily at the top of the archive. Zipping a folder is the ordinary way
	// to make one of these, and it produces `<folder>/index.html` rather than `index.html` — so
	// insisting on the top level rejected archives that were perfectly good.
	root, err := findExportRoot(tempDir)
	if err != nil {
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
		// Whatever directory held the export becomes the served root, so a wrapping folder leaves no
		// trace in the URL: the game is at /games/<slug>/ either way.
		if err := os.Rename(root, final); err != nil {
			return apierror.Internal("Could not install the game")
		}
		return nil
	}
	return commit, cleanup, nil
}

// exportMarkers are the files that make a directory a Godot web export.
var exportMarkers = []string{"index.html", "index.wasm"}

// isExport reports whether dir holds a complete export.
func isExport(dir string) bool {
	for _, marker := range exportMarkers {
		info, err := os.Stat(filepath.Join(dir, marker))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// findExportRoot locates the export inside an unpacked archive.
//
// Searched breadth first, two levels below the top, because there is no reason to go deeper and
// every level costs a directory read. The first level with a match wins, so a zip containing both
// `game/index.html` and `game/extra/index.html` resolves to the outer one, which is the one the
// caller meant.
//
// More than one match at the same level is refused rather than guessed at: picking one at random
// would publish the wrong game under the right name, and that is worse than a message.
func findExportRoot(staged string) (string, error) {
	if isExport(staged) {
		return staged, nil
	}

	level := []string{staged}
	for depth := 0; depth < 2; depth++ {
		var found []string
		var next []string
		for _, dir := range level {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				child := filepath.Join(dir, entry.Name())
				next = append(next, child)
				if isExport(child) {
					found = append(found, child)
				}
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			names := make([]string, 0, len(found))
			for _, dir := range found {
				names = append(names, filepath.Base(dir))
			}
			return "", apierror.InvalidInput(
				"That archive holds more than one web export (" + strings.Join(names, ", ") + "). Zip just one.")
		}
		level = next
	}

	// Say what was in there instead. "Missing index.html" is true but unhelpful when the file exists
	// one directory down, and it sends the reader looking for a problem that is not there.
	return "", apierror.InvalidInput(
		"That archive does not look like a web export — it needs index.html and index.wasm. It contains: " +
			describeTopLevel(staged))
}

// describeTopLevel lists what an archive unpacked to, for the message above.
func describeTopLevel(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return "nothing"
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
		if len(names) == 6 {
			names = append(names, "…")
			break
		}
	}
	return strings.Join(names, ", ")
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

	// Whether these files are an export is decided by findExportRoot, which looks inside a wrapping
	// folder as well as at the top. Checking only the top here is what rejected valid archives.
	return nil
}
