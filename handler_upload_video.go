package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

type VideoInformation struct {
	Data []StreamInfo `json:"streams"`
}

type StreamInfo struct {
	AspectRatio string `json:"display_aspect_ratio"`
}

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

	//videoFile, err := os.CreateTemp("", "tubely-upload.mp4")
	videoFile, err := os.CreateTemp("/tmp/", "tubely-upload.mp4")
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

	aspect, err := getVideoAspectRatio(videoFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to fetch aspect ratio", err)
		return
	}

	fastProcessed, err := processVideoForFastStart(videoFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update processing bit", err)
		return
	}

	fastProcessedVideoFile, err := os.Open(fastProcessed)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to open processed video", err)
		return
	}
	defer os.Remove(fastProcessedVideoFile.Name()) // clean up
	defer fastProcessedVideoFile.Close()

	fileType := strings.TrimPrefix(mediaType, "video/")

	// Generate new filename for each image
	key := make([]byte, 32)
	rand.Read(key)
	// base64.URLEncoding.EncodeToString(key)
	var aspectString string
	if aspect == "16:9" {
		aspectString = "landscape"
	} else if aspect == "9:16" {
		aspectString = "portrait"
	} else {
		aspectString = "other"
	}
	videoFilename := fmt.Sprintf("%s/%s.%s", aspectString, base64.URLEncoding.EncodeToString(key), fileType)

	awsObject := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoFilename,
		Body:        fastProcessedVideoFile,
		ContentType: &checkedMediaType,
	}

	_, videoUploadErr := cfg.s3client.PutObject(context.Background(), &awsObject)
	if videoUploadErr != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video to AWS", videoUploadErr)
		return
	}

	//newURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, videoFilename)
	newURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, videoFilename)

	videoData.VideoURL = &newURL

	videoUpdate := cfg.db.UpdateVideo(videoData)
	if videoUpdate != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video url", videoUpdate)
		return
	}

	videoData, err = cfg.dbVideoToSignedVideo(videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to build presigned URL in subprocess", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoData)
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {

	psClient := s3.NewPresignClient(s3Client)

	params := s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}
	expireFunc := s3.WithPresignExpires(expireTime)
	psRequest, err := psClient.PresignGetObject(context.Background(), &params, expireFunc)
	if err != nil {
		log.Println("Error presigning request")
		return "", err
	}
	return psRequest.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if *video.VideoURL != "" {
		log.Println(*video.VideoURL)
		currentURLString := video.VideoURL
		currentURLData := strings.Split(*currentURLString, ",")
		if len(currentURLData) > 1 {
			bucket := currentURLData[0]
			key := currentURLData[1]
			newURLString, err := generatePresignedURL(&cfg.s3client, bucket, key, time.Duration(10*time.Minute))
			if err != nil {
				log.Println("Error gettings presigned URL")
				return video, err
			}
			video.VideoURL = &newURLString
			return video, nil
		}
	}
	return video, nil
}

func getVideoAspectRatio(filepath string) (string, error) {
	ffProbe := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	var ffProbeOut bytes.Buffer
	ffProbe.Stdout = &ffProbeOut

	err := ffProbe.Run()
	if err != nil {
		log.Println(err)
		return "", fmt.Errorf("")
	}

	decoder := json.NewDecoder(&ffProbeOut)
	results := VideoInformation{}
	videoSuccess := decoder.Decode(&results)
	if videoSuccess != nil {
		return "", videoSuccess
	}
	if len(results.Data) < 1 {
		return "", fmt.Errorf("Video data set empty")
	}

	if results.Data[0].AspectRatio != "16:9" && results.Data[0].AspectRatio != "9:16" {
		return "other", nil
	}

	return results.Data[0].AspectRatio, nil
}

func processVideoForFastStart(filepath string) (string, error) {
	outputFilepath := fmt.Sprintf("%s.processing", filepath)
	ffmpeg := exec.Command("ffmpeg", "-i", filepath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilepath)
	var ffmpegOut bytes.Buffer
	ffmpeg.Stdout = &ffmpegOut

	err := ffmpeg.Run()
	if err != nil {
		log.Println(err)
		return "", fmt.Errorf("")
	}

	return outputFilepath, nil
}
