package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/tigerowo/infinite-canvas/config"
	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/repository"
	"gorm.io/gorm"
)

const (
	generatedMediaDirectoryMode os.FileMode = 0o755
	generatedMediaFileMode      os.FileMode = 0o644
)

var (
	// ErrGeneratedMediaCleaned 用于让内容接口明确返回 410，而不是伪装成普通 404。
	ErrGeneratedMediaCleaned  = errors.New("图片已被清理")
	generatedMediaCleanupOnce sync.Once
)

// GeneratedMediaView 是前端展示生成图片所需的最小存储信息。
type GeneratedMediaView struct {
	// ID 是生成图片记录 ID。
	ID string `json:"id"`
	// URL 是当前可展示地址；已清理时为空。
	URL string `json:"url"`
	// StorageKey 使用 local: 或 server: 前缀区分本地文件和云端对象。
	StorageKey string `json:"storageKey"`
	// StorageStatus 表示 local、cloud 或 cleaned。
	StorageStatus string `json:"storageStatus"`
	// StorageMessage 是自动上传失败或本地清理提示。
	StorageMessage string `json:"storageMessage,omitempty"`
	// Width 是图片像素宽度。
	Width int `json:"width"`
	// Height 是图片像素高度。
	Height int `json:"height"`
	// Bytes 是图片字节数。
	Bytes int64 `json:"bytes"`
	// MimeType 是图片媒体类型。
	MimeType string `json:"mimeType"`
}

// GeneratedMediaContent 是生成图片内容接口的读取结果。
type GeneratedMediaContent struct {
	// Media 是内容对应的生成图片元数据。
	Media model.GeneratedMedia
	// Data 是需要直接响应的图片字节；云端重定向时为空。
	Data []byte
	// RedirectURL 是公开云端地址；为空时由接口直接输出 Data。
	RedirectURL string
}

// CreateGeneratedMedia 先落本地文件，再按配置尝试上传云端。
func CreateGeneratedMedia(ctx context.Context, filename string, contentType string, data []byte, width int, height int, autoUpload bool, provider *StorageObjectProviderInput) (GeneratedMediaView, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return GeneratedMediaView{}, errors.New("请先登录")
	}
	if len(data) == 0 {
		return GeneratedMediaView{}, errors.New("图片内容为空")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/") {
		return GeneratedMediaView{}, errors.New("仅支持保存图片文件")
	}
	media, err := writeGeneratedMedia(user.ID, filename, contentType, data, width, height)
	if err != nil {
		return GeneratedMediaView{}, err
	}
	if !autoUpload {
		return generatedMediaView(ctx, media), nil
	}
	uploaded, err := uploadGeneratedMediaRecord(ctx, media, provider)
	if err == nil {
		return generatedMediaView(ctx, uploaded), nil
	}
	// 云端故障不能抹掉已经成功生成并落盘的图片，本地记录仍然可用。
	media.StorageMessage = "自动上传云端失败：" + err.Error()
	media.UpdatedAt = now()
	_, _ = repository.SaveGeneratedMedia(media)
	return generatedMediaView(ctx, media), nil
}

// UploadGeneratedMediaToCloud 将本地生成图片上传云端，成功后删除本地副本。
func UploadGeneratedMediaToCloud(ctx context.Context, id string, provider *StorageObjectProviderInput) (GeneratedMediaView, error) {
	media, err := currentUserGeneratedMedia(ctx, id)
	if err != nil {
		return GeneratedMediaView{}, err
	}
	if media.StorageStatus == model.GeneratedMediaStatusCleaned {
		return generatedMediaView(ctx, media), ErrGeneratedMediaCleaned
	}
	if media.StorageStatus == model.GeneratedMediaStatusCloud {
		view := generatedMediaView(ctx, media)
		if err := syncCanvasImageTasksAfterGeneratedMediaUpload(ctx, media.UserID, media.ID, view); err != nil {
			log.Printf("repair canvas image task after generated media upload %s failed: %v", media.ID, err)
		}
		return view, nil
	}
	uploaded, err := uploadGeneratedMediaRecord(ctx, media, provider)
	if err != nil {
		return GeneratedMediaView{}, err
	}
	return generatedMediaView(ctx, uploaded), nil
}

// ReadGeneratedMediaContent 读取本地文件；云端记录则复用对象存储下载逻辑。
func ReadGeneratedMediaContent(ctx context.Context, id string) (GeneratedMediaContent, error) {
	media, err := currentUserGeneratedMedia(ctx, id)
	if err != nil {
		return GeneratedMediaContent{}, err
	}
	if media.StorageStatus == model.GeneratedMediaStatusCleaned {
		return GeneratedMediaContent{}, ErrGeneratedMediaCleaned
	}
	if media.StorageStatus == model.GeneratedMediaStatusCloud && media.StorageObjectID != "" {
		download, err := DownloadStorageObject(ctx, media.StorageObjectID)
		if err != nil {
			return GeneratedMediaContent{}, err
		}
		return GeneratedMediaContent{Media: media, Data: download.Data, RedirectURL: download.RedirectURL}, nil
	}
	if strings.TrimSpace(media.RelativePath) == "" {
		markGeneratedMediaCleaned(media)
		return GeneratedMediaContent{}, ErrGeneratedMediaCleaned
	}
	path, err := generatedMediaAbsolutePath(media.RelativePath)
	if err != nil {
		return GeneratedMediaContent{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		markGeneratedMediaCleaned(media)
		return GeneratedMediaContent{}, ErrGeneratedMediaCleaned
	}
	if err != nil {
		return GeneratedMediaContent{}, generatedMediaStorageFailure("服务器无法读取生成图片，请检查 GENERATED_MEDIA_DIR 和文件权限", err)
	}
	// 顺手修复旧版本创建的 0600 文件，避免容器重启或多进程切换用户后再次无法读取。
	if err := os.Chmod(path, generatedMediaFileMode); err != nil {
		log.Printf("normalize generated media %s permission failed: %v", media.ID, err)
	}
	return GeneratedMediaContent{Media: media, Data: data}, nil
}

// DeleteGeneratedMediaFile 删除用户主动移除的生成图片及其元数据。
func DeleteGeneratedMediaFile(ctx context.Context, id string, provider *StorageObjectProviderInput) error {
	media, err := currentUserGeneratedMedia(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if media.StorageStatus == model.GeneratedMediaStatusCloud && media.StorageObjectID != "" {
		if err := DeleteStorageObject(ctx, media.StorageObjectID, provider); err != nil {
			return err
		}
	} else if media.RelativePath != "" {
		if path, pathErr := generatedMediaAbsolutePath(media.RelativePath); pathErr == nil {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return removeErr
			}
		}
	}
	return repository.DeleteGeneratedMedia(media.UserID, media.ID)
}

// StartGeneratedMediaCleanupScheduler 启动本地生成图片保留期清理任务。
func StartGeneratedMediaCleanupScheduler() {
	generatedMediaCleanupOnce.Do(func() {
		scheduler := cron.New()
		if _, err := scheduler.AddFunc("0 3 * * *", cleanupExpiredGeneratedMedia); err != nil {
			log.Printf("start generated media cleanup scheduler failed: %v", err)
			return
		}
		scheduler.Start()
		go cleanupExpiredGeneratedMedia()
	})
}

func cleanupExpiredGeneratedMedia() {
	retentionDays := config.Cfg.GeneratedMediaRetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}
	before := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	for {
		items, err := repository.ListExpiredLocalGeneratedMedia(before, 200)
		if err != nil {
			log.Printf("list expired generated media failed: %v", err)
			return
		}
		if len(items) == 0 {
			return
		}
		for _, media := range items {
			path, pathErr := generatedMediaAbsolutePath(media.RelativePath)
			if pathErr != nil {
				// 非法路径无法安全删除，但必须终结 local 状态，避免清理任务永久空转。
				log.Printf("resolve generated media path failed: %v", pathErr)
				markGeneratedMediaCleaned(media)
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("cleanup generated media %s failed: %v", media.ID, err)
				continue
			}
			markGeneratedMediaCleaned(media)
		}
	}
}

func writeGeneratedMedia(userID string, filename string, contentType string, data []byte, width int, height int) (model.GeneratedMedia, error) {
	id := uuid.NewString()
	ext := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	if ext == "" {
		ext = extensionForContentType(contentType)
	}
	if ext == "" {
		ext = ".img"
	}
	nowTime := time.Now()
	relativePath := filepath.Join(nowTime.Format("2006"), nowTime.Format("01"), nowTime.Format("02"), id+ext)
	absolutePath, err := generatedMediaAbsolutePath(relativePath)
	if err != nil {
		return model.GeneratedMedia{}, err
	}
	if err := ensureGeneratedMediaDirectory(filepath.Dir(absolutePath)); err != nil {
		return model.GeneratedMedia{}, generatedMediaStorageFailure("生成图片无法写入服务器，请检查 GENERATED_MEDIA_DIR 和目录权限", err)
	}
	temporaryPath := absolutePath + ".tmp"
	if err := os.WriteFile(temporaryPath, data, 0o600); err != nil {
		_ = os.Remove(temporaryPath)
		return model.GeneratedMedia{}, generatedMediaStorageFailure("生成图片无法写入服务器，请检查 GENERATED_MEDIA_DIR 和目录权限", err)
	}
	// 临时文件写完后再开放读取权限，避免其他进程读到未完成内容；显式 Chmod 不受 umask 影响。
	if err := os.Chmod(temporaryPath, generatedMediaFileMode); err != nil {
		_ = os.Remove(temporaryPath)
		return model.GeneratedMedia{}, generatedMediaStorageFailure("生成图片权限设置失败，请检查 GENERATED_MEDIA_DIR 所在文件系统", err)
	}
	if err := os.Rename(temporaryPath, absolutePath); err != nil {
		_ = os.Remove(temporaryPath)
		return model.GeneratedMedia{}, generatedMediaStorageFailure("生成图片无法写入服务器，请检查 GENERATED_MEDIA_DIR 和目录权限", err)
	}
	fileName := filepath.Base(strings.TrimSpace(filename))
	if fileName == "" || fileName == "." {
		fileName = "image" + ext
	}
	current := now()
	media := model.GeneratedMedia{
		ID: id, UserID: userID, RelativePath: relativePath, FileName: fileName, MimeType: contentType,
		Bytes: int64(len(data)), Width: width, Height: height, StorageStatus: model.GeneratedMediaStatusLocal,
		CreatedAt: current, UpdatedAt: current,
	}
	if _, err := repository.SaveGeneratedMedia(media); err != nil {
		_ = os.Remove(absolutePath)
		return model.GeneratedMedia{}, err
	}
	return media, nil
}

func uploadGeneratedMediaRecord(ctx context.Context, media model.GeneratedMedia, provider *StorageObjectProviderInput) (model.GeneratedMedia, error) {
	path, err := generatedMediaAbsolutePath(media.RelativePath)
	if err != nil {
		return model.GeneratedMedia{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		markGeneratedMediaCleaned(media)
		return model.GeneratedMedia{}, ErrGeneratedMediaCleaned
	}
	if err != nil {
		return model.GeneratedMedia{}, err
	}
	uploaded, err := UploadStorageObjectWithProvider(ctx, media.FileName, media.MimeType, data, provider)
	if err != nil {
		return model.GeneratedMedia{}, err
	}
	media.StorageObjectID = uploaded.ID
	media.CloudURL = uploaded.URL
	media.StorageStatus = model.GeneratedMediaStatusCloud
	media.StorageMessage = ""
	media.UpdatedAt = now()
	if _, err := repository.SaveGeneratedMedia(media); err != nil {
		return model.GeneratedMedia{}, err
	}
	view := generatedMediaView(ctx, media)
	if err := syncCanvasImageTasksAfterGeneratedMediaUpload(ctx, media.UserID, media.ID, view); err != nil {
		// 云端对象和 generated_media 已经成功，任务冗余字段同步失败不能把一次成功上传伪装成失败。
		log.Printf("sync canvas image task after generated media upload %s failed: %v", media.ID, err)
	}
	// 数据库已经切换为云端后再删本地文件，删除失败只造成冗余，不会让图片不可访问。
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("remove uploaded generated media %s failed: %v", media.ID, err)
	}
	return media, nil
}

func currentUserGeneratedMedia(ctx context.Context, id string) (model.GeneratedMedia, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return model.GeneratedMedia{}, errors.New("请先登录")
	}
	return repository.GetGeneratedMedia(user.ID, strings.TrimSpace(id))
}

func generatedMediaView(ctx context.Context, media model.GeneratedMedia) GeneratedMediaView {
	view := GeneratedMediaView{
		ID: media.ID, StorageStatus: media.StorageStatus, StorageMessage: media.StorageMessage,
		Width: media.Width, Height: media.Height, Bytes: media.Bytes, MimeType: media.MimeType,
	}
	switch media.StorageStatus {
	case model.GeneratedMediaStatusCloud:
		view.URL = absoluteAppURL(ctx, media.CloudURL)
		if view.URL == "" && media.StorageObjectID != "" {
			// 私有对象存储没有公开 URL 时，仍返回完整站内内容地址，避免前端拿到空图。
			view.URL = absoluteAppURL(ctx, "/api/files/"+media.StorageObjectID+"/content")
		}
		view.StorageKey = "server:" + media.StorageObjectID
	case model.GeneratedMediaStatusCleaned:
		view.StorageKey = "local:" + media.ID
		if view.StorageMessage == "" {
			view.StorageMessage = ErrGeneratedMediaCleaned.Error()
		}
	default:
		view.URL = absoluteAppURL(ctx, "/api/v1/generated-images/"+media.ID+"/content")
		view.StorageKey = "local:" + media.ID
	}
	return view
}

func markGeneratedMediaCleaned(media model.GeneratedMedia) {
	current := now()
	media.StorageStatus = model.GeneratedMediaStatusCleaned
	media.StorageMessage = ErrGeneratedMediaCleaned.Error()
	media.CleanedAt = current
	media.UpdatedAt = current
	_, _ = repository.SaveGeneratedMedia(media)
}

func generatedMediaAbsolutePath(relativePath string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("生成图片相对路径无效")
	}
	absoluteRoot, err := generatedMediaRoot()
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(filepath.Join(absoluteRoot, filepath.Clean(relativePath)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("生成图片路径越界")
	}
	return absolutePath, nil
}

func generatedMediaStorageFailure(message string, err error) error {
	log.Printf("generated media storage failed: %v", err)
	return safeMessageError{message: message}
}

func generatedMediaRoot() (string, error) {
	root := strings.TrimSpace(config.Cfg.GeneratedMediaDir)
	if root == "" {
		root = "data/generated-images"
	}
	return filepath.Abs(root)
}

func ensureGeneratedMediaDirectory(directory string) error {
	absoluteRoot, err := generatedMediaRoot()
	if err != nil {
		return err
	}
	relativeDirectory, err := filepath.Rel(absoluteRoot, directory)
	if err != nil || relativeDirectory == ".." || strings.HasPrefix(relativeDirectory, ".."+string(filepath.Separator)) {
		return fmt.Errorf("生成图片目录越界")
	}
	if err := os.MkdirAll(directory, generatedMediaDirectoryMode); err != nil {
		return fmt.Errorf("创建生成图片目录失败: %w", err)
	}
	// MkdirAll 的 mode 会被 umask 过滤，并且不会修复已有目录，因此逐层显式校正到可遍历权限。
	for current := directory; ; current = filepath.Dir(current) {
		if err := os.Chmod(current, generatedMediaDirectoryMode); err != nil {
			return fmt.Errorf("设置生成图片目录权限失败: %w", err)
		}
		if current == absoluteRoot {
			return nil
		}
	}
}
