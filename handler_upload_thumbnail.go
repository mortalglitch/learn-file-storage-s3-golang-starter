package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	thumbnailFile, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to read image", err)
		return
	}

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to fetch video with matching ID", err)
		return
	}

	if userID != videoData.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized to upload thumbnail to this video", err)
		return
	}

	newThumbnail := thumbnail{
		data:      thumbnailFile,
		mediaType: mediaType,
	}

	videoThumbnails[videoID] = newThumbnail

	newURL := fmt.Sprintf("http://localhost:%s/api/thumbnails/%s", cfg.port, videoIDString)

	videoData.ThumbnailURL = &newURL

	videoUpdate := cfg.db.UpdateVideo(videoData)
	if videoUpdate != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video thumbnail", videoUpdate)
	}

	respondWithJSON(w, http.StatusOK, videoData)
}
