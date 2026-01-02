package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading video", videoID, "by user", userID)

	const maxMemory = 10 << 30
	r.ParseMultipartForm(maxMemory)
	http.MaxBytesReader(w, r.Body, maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")

	checkedMediaType, _, err := mime.ParseMediaType(mediaType)
	if checkedMediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Use only mp4", err)
		return
	}

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to fetch video with matching ID", err)
		return
	}

	if userID != videoData.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized to upload this video", err)
		return
	}

	videoFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create file storage", err)
		return
	}
	defer os.Remove(videoFile.Name()) // clean up
	defer videoFile.Close()

	if _, err := io.Copy(videoFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save video", err)
		return
	}

	videoFile.Seek(0, io.SeekStart)

	fileType := strings.TrimPrefix(mediaType, "video/")

	// Generate new filename for each image
	key := make([]byte, 32)
	rand.Read(key)
	// base64.URLEncoding.EncodeToString(key)

	videoFilename := fmt.Sprintf("%s.%s", base64.URLEncoding.EncodeToString(key), fileType)

	awsObject := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoFilename,
		Body:        videoFile,
		ContentType: &checkedMediaType,
	}

	_, videoUploadErr := cfg.s3client.PutObject(context.Background(), &awsObject)
	if videoUploadErr != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to AWS", videoUploadErr)
		return
	}

	newURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, videoFilename)

	videoData.VideoURL = &newURL

	videoUpdate := cfg.db.UpdateVideo(videoData)
	if videoUpdate != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video url", videoUpdate)
	}

	respondWithJSON(w, http.StatusOK, videoData)
}
