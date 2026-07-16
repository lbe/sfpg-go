package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/lbe/sfpg-go/internal/server/ui"
)

// LightboxByID handles GET /lightbox/{id}, returning the lightbox view HTML with ETag caching.
// ConditionalMiddleware generates 304 Not Modified on ETag match.
func (h *GalleryHandlers) LightboxByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fileID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.ServerError(w, r, fmt.Errorf("invalid file id: %s", idStr))
		return
	}

	qh, _, put, err := h.getQueries()
	if err != nil {
		h.ServerError(w, r, err)
		return
	}
	defer put()

	file, err := qh.GetFileViewByID(h.Ctx, fileID)
	if h.handleDBError(w, r, err) {
		return
	}

	etagVersion := h.galleryOps.GetETagVersion()
	etag := fmt.Sprintf("\"%s-%d\"", etagVersion, fileID)
	h.setCacheHeaders(w, etag)
	// Do NOT set HX-Push-URL for lightbox: opening the lightbox should not create a history
	// entry, so that after closing, back/j goes to the previous folder (desired behavior at 0d6377c).

	images, err := qh.GetFileViewsByFolderIDOrderByFileName(h.Ctx, file.FolderID)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	imageCount := len(images)
	if imageCount == 0 {
		http.NotFound(w, r)
		return
	}

	currentIndex := -1
	for i, img := range images {
		if img.ID == fileID {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		h.ServerError(w, r, fmt.Errorf("could not find file in folder view"))
		return
	}

	folder, err := qh.GetFolderViewByID(h.Ctx, file.FolderID.Int64)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	breadcrumbs, err := h.generateBreadcrumbsByID(file.FolderID.Int64)
	if err != nil {
		h.ServerError(w, r, err)
		return
	}

	data := LightboxData{
		GalleryName:    folder.Name,
		CurrentImageID: fileID,
		CurrentIndex:   currentIndex,
		ImageCount:     imageCount,
		FirstIndex:     int(images[0].ID),
		LastIndex:      int(images[imageCount-1].ID),
		Breadcrumbs:    breadcrumbs,
	}

	if imageCount > 1 {
		data.HasPrev = true
		if currentIndex > 0 {
			data.PrevIndex = int(images[currentIndex-1].ID)
		} else {
			data.PrevIndex = int(images[imageCount-1].ID)
		}
		data.PreloadPrevPath = fmt.Sprintf("/raw-image/%d", data.PrevIndex)
	}
	if imageCount > 1 {
		data.HasNext = true
		if currentIndex < imageCount-1 {
			data.NextIndex = int(images[currentIndex+1].ID)
		} else {
			data.NextIndex = int(images[0].ID)
		}
		data.PreloadNextPath = fmt.Sprintf("/raw-image/%d", data.NextIndex)
	}

	if err := ui.RenderTemplate(w, "lightbox-content.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
	}
}
