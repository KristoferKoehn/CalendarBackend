package httpapi

import (
	"errors"
	"mime/multipart"
	"net/http"
	"strings"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/projects"
)

// Project endpoints.
//
// Reading is public and writing is admin-only, because a project list is what the landing page
// shows to everyone. The admin check lives in the store alongside every other rule about projects,
// so the transport cannot disagree with it.

type projectsResponse struct {
	Projects []projects.Project `json:"projects"`
}

type projectResponse struct {
	Project projects.Project `json:"project"`
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		// A deployment without a projects directory still serves the app; it just has no cards.
		writeJSON(w, http.StatusOK, projectsResponse{Projects: []projects.Project{}})
		return
	}
	list, err := h.projects.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectsResponse{Projects: list})
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeError(w, apierror.NotFound("No such project"))
		return
	}
	project, err := h.projects.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectResponse{Project: project})
}

// createProject accepts a form and, optionally, a zip of the game build.
//
// One endpoint for both because they are one action from the admin's point of view — fill in the
// card, attach the export, done. A project with no build is still meaningful: the calendar is not a
// game and its files are not ours to upload.
func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeError(w, apierror.Internal("Projects are not configured on this server"))
		return
	}

	// The ceiling is applied to the body before it is parsed, so an oversized upload is refused
	// while it is still arriving rather than after it has been buffered.
	r.Body = http.MaxBytesReader(w, r.Body, projects.MaxUploadBytes)

	// 16 MB stays in memory and the rest spills to a temporary file, which is enough for a small
	// export and does not hold a large one in RAM.
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, apierror.InvalidInput("That upload is larger than this server accepts"))
			return
		}
		writeError(w, apierror.InvalidInput("Expected a multipart form with the project details"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()

	input := projects.CreateInput{
		Name:        r.FormValue("name"),
		Slug:        r.FormValue("slug"),
		Description: r.FormValue("description"),
		Status:      projects.Status(strings.TrimSpace(r.FormValue("status"))),
		Stack:       splitList(r.FormValue("stack")),
		When:        r.FormValue("when"),
		Href:        r.FormValue("href"),
		Delivery:    projects.Delivery(strings.TrimSpace(r.FormValue("delivery"))),
	}

	// The slug is needed before staging, because it names where the upload is installed.
	if input.Slug == "" {
		input.Slug = projects.Slugify(input.Name)
	}
	if err := projects.ValidateSlug(input.Slug); err != nil {
		writeError(w, err)
		return
	}

	var commit func() error
	var cleanup func()
	if archive, size, err := gameUpload(r); err != nil {
		writeError(w, err)
		return
	} else if archive != nil {
		defer archive.Close()
		if size > projects.MaxUploadBytes {
			writeError(w, apierror.InvalidInput("That file is larger than this server accepts"))
			return
		}
		// One upload, two destinations. A playable project is unpacked and validated as a web export;
		// a download is kept exactly as uploaded, because the point of it is to hand over the file
		// the admin made rather than a copy this server rebuilt.
		//
		// `archive` is a multipart.File, which is both a Reader and a ReaderAt: unpacking needs the
		// latter because a zip is read from its central directory at the end.
		if input.Delivery == projects.DeliveryDownload {
			commit, cleanup, err = h.projects.StageArchive(input.Slug, archive, size)
		} else {
			commit, cleanup, err = h.projects.StageBuild(input.Slug, archive, size)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		defer cleanup()
		input.HasGame = true
	}

	project, err := h.projects.Create(r.Context(), actorFrom(r), input)
	if err != nil {
		writeError(w, err)
		return
	}

	// The build is installed only after the card exists can be rolled back: if this fails the
	// project is removed again, so a card never points at files that are not there.
	if commit != nil {
		if err := commit(); err != nil {
			if _, undoErr := h.projects.Delete(r.Context(), actorFrom(r), project.Slug); undoErr != nil {
				h.logger.Error("could not remove a project after its build failed to install",
					"slug", project.Slug, "error", undoErr)
			}
			writeError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusCreated, projectResponse{Project: project})
}

// updateProject changes a card's details. The build is deliberately not replaceable here: swapping
// the files is a separate upload, and mixing the two would mean a failed upload could leave a
// description describing something that is not there.
func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeError(w, apierror.NotFound("No such project"))
		return
	}
	var req struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Status      *string   `json:"status"`
		Stack       *[]string `json:"stack"`
		When        *string   `json:"when"`
		Href        *string   `json:"href"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	input := projects.UpdateInput{
		Name:        req.Name,
		Description: req.Description,
		When:        req.When,
		Href:        req.Href,
	}
	if req.Status != nil {
		status := projects.Status(*req.Status)
		input.Status = &status
	}
	if req.Stack != nil {
		input.Stack = *req.Stack
		input.StackSet = true
	}

	project, err := h.projects.Update(r.Context(), actorFrom(r), r.PathValue("slug"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectResponse{Project: project})
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeError(w, apierror.NotFound("No such project"))
		return
	}
	project, err := h.projects.Delete(r.Context(), actorFrom(r), r.PathValue("slug"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectResponse{Project: project})
}

// gameUpload returns the uploaded archive, or nil when the form carried none.
//
// The form field is called "game". An empty file input in a browser sends a part with no filename,
// which is treated as no upload rather than as a zero-byte zip — otherwise submitting the form
// without choosing a file would fail with "not a readable zip".
func gameUpload(r *http.Request) (multipart.File, int64, error) {
	if r.MultipartForm == nil {
		return nil, 0, nil
	}
	headers := r.MultipartForm.File["game"]
	if len(headers) == 0 {
		return nil, 0, nil
	}
	header := headers[0]
	if header.Filename == "" || header.Size == 0 {
		return nil, 0, nil
	}

	file, err := header.Open()
	if err != nil {
		return nil, 0, apierror.InvalidInput("Could not read the uploaded file")
	}
	return file, header.Size, nil
}

// splitList reads a comma-separated form field into entries. A form sends one string per field;
// repeating the field would be tidier for an API client and is not something an HTML form does.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}
