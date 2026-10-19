package httpapi

import (
	"mime/multipart"
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/files"
)

// The file shelf: a directory the admin uploads files to and links out from.
//
// All three endpoints are admin-only, because this is a place to put things rather than a public
// library — the files themselves are served publicly by the web app, which is the point of the links.

type filesResponse struct {
	Files []files.File `json:"files"`
}

type fileResponse struct {
	File files.File `json:"file"`
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request) {
	if h.files == nil {
		writeError(w, apierror.NotFound("Files are not configured on this server"))
		return
	}
	list, err := h.files.List(r.Context(), actorFrom(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, filesResponse{Files: list})
}

// uploadFile accepts one file and stores it.
//
// The name comes from the upload unless the form overrides it, which the dashboard offers because a
// file exported from a tool is often called something unhelpful.
func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	if h.files == nil {
		writeError(w, apierror.NotFound("Files are not configured on this server"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxFileBytes)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, apierror.InvalidInput("Expected a multipart form with a single file"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()

	header := firstUpload(r, "file")
	if header == nil {
		writeError(w, apierror.InvalidInput("Choose a file to upload"))
		return
	}
	upload, err := header.Open()
	if err != nil {
		writeError(w, apierror.InvalidInput("Could not read the uploaded file"))
		return
	}
	defer upload.Close()

	name := r.FormValue("name")
	if name == "" {
		name = header.Filename
	}

	stored, err := h.files.Save(r.Context(), actorFrom(r), upload, name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, fileResponse{File: stored})
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	if h.files == nil {
		writeError(w, apierror.NotFound("Files are not configured on this server"))
		return
	}
	removed, err := h.files.Delete(r.Context(), actorFrom(r), r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileResponse{File: removed})
}

// firstUpload returns the named file part, or nil when the form carried none.
func firstUpload(r *http.Request, field string) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	headers := r.MultipartForm.File[field]
	if len(headers) == 0 || headers[0].Filename == "" || headers[0].Size == 0 {
		return nil
	}
	return headers[0]
}
