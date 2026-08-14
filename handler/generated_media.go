package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tigerowo/infinite-canvas/service"
)

// SaveGeneratedImage 保存生成图片，自动上传开启时由后端完成云端同步。
func SaveGeneratedImage(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		Fail(w, "请选择要保存的图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		FailError(w, err)
		return
	}
	contentType := header.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(data)
	}
	width, _ := strconv.Atoi(r.FormValue("width"))
	height, _ := strconv.Atoi(r.FormValue("height"))
	autoUpload, _ := strconv.ParseBool(r.FormValue("autoUpload"))
	provider, err := generatedMediaProvider(r.FormValue("provider"))
	if err != nil {
		Fail(w, err.Error())
		return
	}
	media, err := service.CreateGeneratedMedia(r.Context(), header.Filename, contentType, data, width, height, autoUpload, provider)
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, media)
}

// UploadGeneratedImage 将本地生成图片手动上传云端。
func UploadGeneratedImage(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		Provider *service.StorageObjectProviderInput `json:"provider"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&request)
	}
	media, err := service.UploadGeneratedMediaToCloud(r.Context(), id, request.Provider)
	if errors.Is(err, service.ErrGeneratedMediaCleaned) {
		FailWithStatus(w, http.StatusGone, service.ErrGeneratedMediaCleaned.Error())
		return
	}
	if err != nil {
		FailError(w, err)
		return
	}
	OK(w, media)
}

// GeneratedImageContent 返回本地生成图片内容；已清理图片返回 410。
func GeneratedImageContent(w http.ResponseWriter, r *http.Request, id string) {
	content, err := service.ReadGeneratedMediaContent(r.Context(), id)
	if errors.Is(err, service.ErrGeneratedMediaCleaned) {
		FailWithStatus(w, http.StatusGone, service.ErrGeneratedMediaCleaned.Error())
		return
	}
	if err != nil {
		FailError(w, err)
		return
	}
	if content.RedirectURL != "" {
		http.Redirect(w, r, content.RedirectURL, http.StatusTemporaryRedirect)
		return
	}
	contentType := strings.TrimSpace(content.Media.MimeType)
	if contentType == "" {
		contentType = http.DetectContentType(content.Data)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(content.Data)
}

// DeleteGeneratedImage 删除本地生成图片及其元数据。
func DeleteGeneratedImage(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		Provider *service.StorageObjectProviderInput `json:"provider"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&request)
	}
	if err := service.DeleteGeneratedMediaFile(r.Context(), id, request.Provider); err != nil {
		FailError(w, err)
		return
	}
	OK(w, true)
}

func generatedMediaProvider(raw string) (*service.StorageObjectProviderInput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var provider service.StorageObjectProviderInput
	if err := json.Unmarshal([]byte(raw), &provider); err != nil {
		return nil, errors.New("用户对象存储配置格式错误")
	}
	return &provider, nil
}
