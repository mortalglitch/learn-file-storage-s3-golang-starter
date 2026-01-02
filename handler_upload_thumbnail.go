package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	checkedMediaType, _, err := mime.ParseMediaType(mediaType)
	if checkedMediaType != "image/jpg" && checkedMediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Use only jpg or png", err)
		return
	}
	// thumbnailFile, err := io.ReadAll(file)
	// if err != nil {
	// 	respondWithError(w, http.StatusBadRequest, "Unable to read image", err)
	// 	return
	// }

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to fetch video with matching ID", err)
		return
	}

	if userID != videoData.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized to upload thumbnail to this video", err)
		return
	}

	//newThumbnail := thumbnail{
	//	data:      thumbnailFile,
	//	mediaType: mediaType,
	//}

	// videoThumbnails[videoID] = newThumbnail

	// thumbnailString := base64.StdEncoding.EncodeToString(thumbnailFile)
	// dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, thumbnailString)

	//newURL := fmt.Sprintf("http://localhost:%s/api/thumbnails/%s", cfg.port, videoIDString)

	// Attempt at saving to disk
	fileType := strings.TrimPrefix(mediaType, "image/")
	thumbnailFilename := fmt.Sprintf("%s.%s", videoIDString, fileType)
	newThumbnailPath := filepath.Join(cfg.assetsRoot, thumbnailFilename)
	newFile, err := os.Create(newThumbnailPath)
	if _, err := io.Copy(newFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save image", err)
		return
	}

	newURL := fmt.Sprintf("http://localhost:%s/%s", cfg.port, newThumbnailPath)

	videoData.ThumbnailURL = &newURL

	videoUpdate := cfg.db.UpdateVideo(videoData)
	if videoUpdate != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video thumbnail", videoUpdate)
	}

	respondWithJSON(w, http.StatusOK, videoData)
}
