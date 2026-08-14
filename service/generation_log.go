package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/repository"
)

const generationLogLimit = 1000

func CurrentUserVideoGenerationLogs(ctx context.Context) ([]json.RawMessage, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return nil, errors.New("请先登录")
	}
	cleanupGenerationLogs()
	logs, err := repository.ListVideoGenerationLogs(user.ID, generationLogLimit)
	if err != nil {
		return nil, err
	}
	return videoGenerationPayloads(logs), nil
}

func SaveCurrentUserVideoGenerationLogs(ctx context.Context, raws []json.RawMessage) ([]json.RawMessage, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return nil, errors.New("请先登录")
	}
	cleanupGenerationLogs()
	logs := make([]model.VideoGenerationLog, 0, len(raws))
	for _, raw := range raws {
		log := videoGenerationLogFromPayload(raw)
		if log.ID != "" {
			logs = append(logs, log)
		}
	}
	if err := repository.UpsertVideoGenerationLogs(user.ID, logs); err != nil {
		return nil, err
	}
	return CurrentUserVideoGenerationLogs(ctx)
}

func DeleteCurrentUserVideoGenerationLog(ctx context.Context, id string) error {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return errors.New("请先登录")
	}
	cleanupGenerationLogs()
	return repository.SoftDeleteVideoGenerationLog(user.ID, strings.TrimSpace(id), now())
}

func DeleteCurrentUserVideoGenerationLogs(ctx context.Context, ids []string) error {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return errors.New("请先登录")
	}
	cleanupGenerationLogs()
	return repository.SoftDeleteVideoGenerationLogs(user.ID, ids, now())
}

func CurrentUserImageGenerationLogs(ctx context.Context) ([]json.RawMessage, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return nil, errors.New("请先登录")
	}
	cleanupGenerationLogs()
	if err := migrateUserImageGenerationLogs(user.ID); err != nil {
		return nil, err
	}
	logs, err := repository.ListImageGenerationLogs(user.ID, generationLogLimit)
	if err != nil {
		return nil, err
	}
	return imageGenerationPayloads(user.ID, logs)
}

func SaveCurrentUserImageGenerationLogs(ctx context.Context, raws []json.RawMessage) error {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return errors.New("请先登录")
	}
	cleanupGenerationLogs()
	logs := make([]model.ImageGenerationLog, 0, len(raws))
	for _, raw := range raws {
		log := imageGenerationLogFromPayload(raw)
		if log.ID != "" {
			logs = append(logs, log)
		}
	}
	return repository.UpsertImageGenerationLogs(user.ID, logs)
}

func DeleteCurrentUserImageGenerationLog(ctx context.Context, id string) error {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return errors.New("请先登录")
	}
	cleanupGenerationLogs()
	return repository.SoftDeleteImageGenerationLog(user.ID, strings.TrimSpace(id), now())
}

func DeleteCurrentUserImageGenerationLogs(ctx context.Context, ids []string) error {
	user, ok := UserFromContext(ctx)
	if !ok || user.ID == "" {
		return errors.New("请先登录")
	}
	cleanupGenerationLogs()
	return repository.SoftDeleteImageGenerationLogs(user.ID, ids, now())
}

func videoGenerationPayloads(logs []model.VideoGenerationLog) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(logs))
	for _, log := range logs {
		if strings.TrimSpace(log.PayloadJSON) != "" {
			result = append(result, json.RawMessage(log.PayloadJSON))
		}
	}
	return result
}

func imageGenerationPayloads(userID string, logs []model.ImageGenerationLog) ([]json.RawMessage, error) {
	records := make([]map[string]any, 0, len(logs))
	localIDs := make([]string, 0)
	for _, log := range logs {
		if strings.TrimSpace(log.SummaryJSON) == "" {
			continue
		}
		record := parseGenerationLogRecord(json.RawMessage(log.SummaryJSON))
		records = append(records, record)
		localIDs = append(localIDs, generationLogLocalMediaIDs(record)...)
	}
	media, err := repository.ListGeneratedMediaByIDs(userID, localIDs)
	if err != nil {
		return nil, err
	}
	mediaByID := make(map[string]model.GeneratedMedia, len(media))
	for _, item := range media {
		mediaByID[item.ID] = item
	}
	result := make([]json.RawMessage, 0, len(records))
	for _, record := range records {
		reconcileGenerationLogMedia(record, mediaByID)
		raw, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		result = append(result, raw)
	}
	return result, nil
}

func videoGenerationLogFromPayload(raw json.RawMessage) model.VideoGenerationLog {
	record := parseGenerationLogRecord(raw)
	task := generationLogRecord(record["task"])
	video := generationLogRecord(record["video"])
	current := now()
	createdAt := generationLogCreatedAt(record)
	if createdAt == "" {
		createdAt = current
	}
	return model.VideoGenerationLog{
		ID:          generationLogString(record["id"]),
		TaskID:      firstGenerationLogValue(generationLogString(task["id"]), generationLogString(task["task_id"]), generationLogString(record["taskId"])),
		VideoID:     firstGenerationLogValue(generationLogString(task["video_id"]), generationLogString(video["id"]), generationLogString(record["videoId"])),
		Status:      generationLogString(record["status"]),
		PayloadJSON: string(raw),
		CreatedAt:   createdAt,
		UpdatedAt:   current,
		DeletedAt:   "",
	}
}

func imageGenerationLogFromPayload(raw json.RawMessage) model.ImageGenerationLog {
	record := parseGenerationLogRecord(raw)
	task := generationLogRecord(record["task"])
	image := firstGenerationLogRecord(record["image"], record["result"], record["output"])
	current := now()
	createdAt := generationLogCreatedAt(record)
	if createdAt == "" {
		createdAt = current
	}
	return model.ImageGenerationLog{
		ID:          generationLogString(record["id"]),
		TaskID:      firstGenerationLogValue(generationLogString(task["id"]), generationLogString(task["task_id"]), generationLogString(record["taskId"])),
		ImageID:     firstGenerationLogValue(generationLogString(image["id"]), generationLogString(image["storageKey"]), generationLogString(image["url"]), generationLogString(record["imageId"])),
		Status:      generationLogString(record["status"]),
		PayloadJSON: string(raw),
		SummaryJSON: imageGenerationSummary(raw),
		CreatedAt:   createdAt,
		UpdatedAt:   current,
		DeletedAt:   "",
	}
}

func imageGenerationSummary(raw json.RawMessage) string {
	record := parseGenerationLogRecord(raw)
	summary := selectGenerationLogFields(record, []string{
		"id", "createdAt", "title", "prompt", "time", "model", "durationMs", "successCount", "failCount",
		"imageCount", "size", "quality", "status", "errors", "categoryIds", "workflowId", "workflowName",
		"workflowTaskId", "lastPolledAt",
	})
	if config := generationLogRecord(record["config"]); len(config) > 0 {
		summary["config"] = selectGenerationLogFields(config, []string{
			"channelMode", "model", "imageModel", "activeChannelId", "imageChannelId", "quality", "size", "count",
			"apiMode", "streamImages", "streamPartialImages", "responseFormatB64Json", "codexCli",
		})
	}
	if images := compactGenerationLogImages(record["images"]); len(images) > 0 {
		summary["images"] = images
	} else {
		summary["images"] = []map[string]any{}
	}
	if references := compactGenerationLogReferences(record["references"]); len(references) > 0 {
		// 重试只需要引用标识和存储键；大体积 data URL 不应进入列表接口。
		summary["references"] = references
	}
	if task := compactGenerationLogTask(record["task"]); len(task) > 0 {
		summary["task"] = task
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func selectGenerationLogFields(record map[string]any, fields []string) map[string]any {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, exists := record[field]; exists && value != nil {
			result[field] = value
		}
	}
	return result
}

func compactGenerationLogImages(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		image := generationLogRecord(item)
		if len(image) == 0 {
			continue
		}
		compact := selectGenerationLogFields(image, []string{
			"id", "dataUrl", "storageKey", "durationMs", "width", "height", "bytes", "mimeType", "storageStatus", "storageMessage",
		})
		if dataURL := generationLogString(compact["dataUrl"]); strings.HasPrefix(dataURL, "data:") || strings.HasPrefix(dataURL, "blob:") {
			delete(compact, "dataUrl")
		}
		result = append(result, compact)
	}
	return result
}

func compactGenerationLogReferences(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		reference := generationLogRecord(item)
		if len(reference) == 0 {
			continue
		}
		compact := selectGenerationLogFields(reference, []string{"id", "name", "type", "storageKey", "source", "temporary", "dataUrl"})
		if dataURL := generationLogString(compact["dataUrl"]); strings.HasPrefix(dataURL, "data:") || strings.HasPrefix(dataURL, "blob:") {
			delete(compact, "dataUrl")
		}
		result = append(result, compact)
	}
	return result
}

func compactGenerationLogTask(value any) map[string]any {
	task := generationLogRecord(value)
	if len(task) == 0 {
		return map[string]any{}
	}
	compact := selectGenerationLogFields(task, []string{
		"id", "parent_task_id", "source", "source_id", "node_id", "channelId", "userChannelId", "channelName", "model",
		"prompt", "status", "progress", "url", "image_url", "image_urls", "storageKey", "width", "height", "mimeType", "bytes",
		"started_at", "startedAt", "created_at", "createdAt", "completed_at", "error", "error_detail",
	})
	for _, field := range []string{"url", "image_url"} {
		if generationLogInlineURL(generationLogString(compact[field])) {
			delete(compact, field)
		}
	}
	if values, ok := compact["image_urls"].([]any); ok {
		urls := make([]string, 0, len(values))
		for _, value := range values {
			url := generationLogString(value)
			if url != "" && !generationLogInlineURL(url) {
				urls = append(urls, url)
			}
		}
		compact["image_urls"] = urls
	}
	return compact
}

func generationLogInlineURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "data:") || strings.HasPrefix(value, "blob:")
}

func generationLogLocalMediaIDs(record map[string]any) []string {
	items, _ := record["images"].([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		key := generationLogString(generationLogRecord(item)["storageKey"])
		if strings.HasPrefix(key, "local:") {
			ids = append(ids, strings.TrimPrefix(key, "local:"))
		}
	}
	return ids
}

func reconcileGenerationLogMedia(record map[string]any, mediaByID map[string]model.GeneratedMedia) {
	items, _ := record["images"].([]any)
	for _, item := range items {
		image := generationLogRecord(item)
		key := generationLogString(image["storageKey"])
		if !strings.HasPrefix(key, "local:") {
			continue
		}
		id := strings.TrimPrefix(key, "local:")
		media, exists := mediaByID[id]
		if !exists {
			image["dataUrl"] = ""
			image["storageStatus"] = model.GeneratedMediaStatusCleaned
			image["storageMessage"] = ErrGeneratedMediaCleaned.Error()
			continue
		}
		view := generatedMediaView(media)
		image["dataUrl"] = view.URL
		image["storageKey"] = view.StorageKey
		image["storageStatus"] = view.StorageStatus
		image["storageMessage"] = view.StorageMessage
		image["width"] = view.Width
		image["height"] = view.Height
		image["bytes"] = view.Bytes
		image["mimeType"] = view.MimeType
	}
}

func parseGenerationLogRecord(raw json.RawMessage) map[string]any {
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return map[string]any{}
	}
	return record
}

func generationLogRecord(value any) map[string]any {
	if record, ok := value.(map[string]any); ok {
		return record
	}
	return map[string]any{}
}

func firstGenerationLogRecord(values ...any) map[string]any {
	for _, value := range values {
		record := generationLogRecord(value)
		if len(record) > 0 {
			return record
		}
	}
	return map[string]any{}
}

func generationLogString(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case float64:
		if item == float64(int64(item)) {
			return strconv.FormatInt(int64(item), 10)
		}
		return fmt.Sprintf("%v", item)
	case bool:
		return strconv.FormatBool(item)
	default:
		return ""
	}
}

func generationLogCreatedAt(record map[string]any) string {
	for _, key := range []string{"createdAt", "created_at", "time"} {
		value := generationLogString(record[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func firstGenerationLogValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cleanupGenerationLogs() {
	before := time.Now().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	_ = repository.CleanupDeletedVideoGenerationLogs(before)
	_ = repository.CleanupDeletedImageGenerationLogs(before)
}

func migrateUserImageGenerationLogs(userID string) error {
	config, found, err := repository.GetUserConfig(userID)
	if err != nil || !found || strings.TrimSpace(config.ImageHistory) == "" {
		return err
	}
	var legacy struct {
		Logs []json.RawMessage `json:"logs"`
	}
	if err := json.Unmarshal([]byte(config.ImageHistory), &legacy); err != nil || len(legacy.Logs) == 0 {
		config.ImageHistory = ""
		_, saveErr := repository.SaveUserConfig(config)
		return saveErr
	}
	logs := make([]model.ImageGenerationLog, 0, len(legacy.Logs))
	for _, raw := range legacy.Logs {
		log := imageGenerationLogFromPayload(raw)
		if log.ID != "" {
			logs = append(logs, log)
		}
	}
	if err := repository.UpsertImageGenerationLogs(userID, logs); err != nil {
		return err
	}
	config.ImageHistory = ""
	_, err = repository.SaveUserConfig(config)
	return err
}
