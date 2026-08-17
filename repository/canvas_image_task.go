package repository

import (
	"strings"

	"github.com/tigerowo/infinite-canvas/model"
	"gorm.io/gorm"
)

func SaveCanvasImageTask(task model.CanvasImageTask) (model.CanvasImageTask, error) {
	db, err := DB()
	if err != nil {
		return task, err
	}
	return task, db.Save(&task).Error
}

func UpdateCanvasImageTask(task model.CanvasImageTask) (model.CanvasImageTask, error) {
	db, err := DB()
	if err != nil {
		return task, err
	}

	return task, db.Model(&model.CanvasImageTask{}).
		Where("user_id = ? AND id = ? AND deleted_at = ''", task.UserID, task.ID).
		Select("*").
		Updates(&task).Error
}

func GetUserCanvasImageTask(userID string, id string) (model.CanvasImageTask, bool, error) {
	db, err := DB()
	if err != nil {
		return model.CanvasImageTask{}, false, err
	}
	var task model.CanvasImageTask
	err = db.First(&task, "user_id = ? AND id = ? AND deleted_at = ''", userID, id).Error
	if err != nil {
		return model.CanvasImageTask{}, false, nil
	}
	return task, true, nil
}

func ListUserCanvasImageTasks(userID string, sources []string, limit int) ([]model.CanvasImageTask, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	var tasks []model.CanvasImageTask
	query := db.Where("user_id = ? AND deleted_at = ''", userID)
	if len(sources) > 0 {
		query = query.Where("source IN ?", sources)
	}
	err = query.
		Where("status IN ?", []string{"queued", "processing", "running", "in_progress"}).
		Order("created_at DESC").
		Limit(limit).
		Find(&tasks).Error
	return tasks, err
}

func BatchUserCanvasImageTasks(userID string, ids []string) ([]model.CanvasImageTask, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	keys := uniqueTrimmedValues(ids...)
	if len(keys) == 0 {
		return []model.CanvasImageTask{}, nil
	}
	var tasks []model.CanvasImageTask
	err = db.Where("user_id = ? AND deleted_at = '' AND id IN ?", userID, keys).Find(&tasks).Error
	return tasks, err
}

func UpdateUserCanvasImageTasksAfterGeneratedMediaUpload(userID string, localStorageKey string, localURL string, localPath string, cloudStorageKey string, cloudURL string, updatedAt string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	localStorageKey = strings.TrimSpace(localStorageKey)
	localURL = strings.TrimSpace(localURL)
	localPath = strings.TrimSpace(localPath)
	cloudStorageKey = strings.TrimSpace(cloudStorageKey)
	cloudURL = strings.TrimSpace(cloudURL)
	fullLikeURL := "%" + localURL + "%"
	pathLikeURL := "%" + localPath + "%"
	// 多图会并发上传；必须在数据库内按当前值原子替换，禁止先查询再整行保存导致另一张图的更新被旧快照覆盖。
	return db.Model(&model.CanvasImageTask{}).
		Where(
			"user_id = ? AND deleted_at = '' AND (storage_key = ? OR image_url IN ? OR image_urls LIKE ? OR image_urls LIKE ? OR response_body LIKE ? OR response_body LIKE ?)",
			strings.TrimSpace(userID),
			localStorageKey,
			[]string{localURL, localPath},
			fullLikeURL,
			pathLikeURL,
			fullLikeURL,
			pathLikeURL,
		).
		Updates(map[string]any{
			"image_url": gorm.Expr(
				"CASE WHEN storage_key = ? OR image_url = ? OR image_url = ? THEN ? ELSE image_url END",
				localStorageKey, localURL, localPath, cloudURL,
			),
			"image_urls": gorm.Expr(
				"REPLACE(REPLACE(image_urls, ?, ?), ?, ?)",
				localURL, cloudURL, localPath, cloudURL,
			),
			"response_body": gorm.Expr(
				"REPLACE(REPLACE(response_body, ?, ?), ?, ?)",
				localURL, cloudURL, localPath, cloudURL,
			),
			"storage_key": gorm.Expr("CASE WHEN storage_key = ? THEN ? ELSE storage_key END", localStorageKey, cloudStorageKey),
			"updated_at":  updatedAt,
		}).Error
}

func DeleteUserCanvasImageTask(userID string, id string, deletedAt string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return softDeleteCanvasImageTasks(
		db.Model(&model.CanvasImageTask{}).
			Where("user_id = ? AND id = ?", userID, strings.TrimSpace(id)),
		deletedAt,
	)
}

func DeleteUserCanvasTasks(userID string, sourceID string, nodeIDs []string, deletedAt string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	nodeIDs = uniqueTrimmedValues(nodeIDs...)

	return db.Transaction(func(tx *gorm.DB) error {
		imageQuery := tx.Model(&model.CanvasImageTask{}).Where(
			"user_id = ? AND source = ? AND source_id = ?",
			userID,
			"canvas",
			sourceID,
		)
		audioQuery := tx.Where(
			"user_id = ? AND source = ? AND source_id = ?",
			userID,
			"canvas",
			sourceID,
		)
		if len(nodeIDs) > 0 {
			imageQuery = imageQuery.Where("node_id IN ?", nodeIDs)
			audioQuery = audioQuery.Where("node_id IN ?", nodeIDs)
		}

		if err := softDeleteCanvasImageTasks(imageQuery, deletedAt); err != nil {
			return err
		}
		return audioQuery.Delete(&model.CanvasAudioTask{}).Error
	})
}

func softDeleteCanvasImageTasks(query *gorm.DB, deletedAt string) error {
	// 旧任务可能仍包含 base64 或完整上游响应；软删除时一并清空，避免无效大字段继续占库。
	return query.Where("deleted_at = ''").Updates(map[string]any{
		"request_body":  "",
		"response_body": "",
		"error_detail":  "",
		"image_url":     "",
		"image_urls":    "[]",
		"deleted_at":    deletedAt,
		"updated_at":    deletedAt,
	}).Error
}

func uniqueTrimmedValues(values ...string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}
