package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/projects"
)

// The downloads inventory.
//
// Separate from both projects and the file shelf. A download is created by a project — it is what the
// card hands over — but it is also a file with a life of its own: an address, a size, a link somebody
// may have pasted somewhere. This is the view that treats it as one.

type downloadsResponse struct {
	Downloads []projects.Download `json:"downloads"`
}

type downloadResponse struct {
	Download projects.Download `json:"download"`
}

func (h *Handler) listDownloads(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeJSON(w, http.StatusOK, downloadsResponse{Downloads: []projects.Download{}})
		return
	}
	list, err := h.projects.ListDownloads(r.Context(), actorFrom(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, downloadsResponse{Downloads: list})
}

// deleteDownload removes an archive no project points at. Refused when one does, because the card
// would then offer a file that is not there.
func (h *Handler) deleteDownload(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeError(w, apierror.NotFound("No such download"))
		return
	}
	removed, err := h.projects.DeleteDownload(r.Context(), actorFrom(r), r.PathValue("slug"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, downloadResponse{Download: removed})
}
