package repository

import (
	"strings"

	"github.com/tigerowo/infinite-canvas/model"
)

// SaveGeneratedMedia 保存生成图片元数据。
func SaveGeneratedMedia(media model.GeneratedMedia) (model.GeneratedMedia, error) {
	db, err := DB()
	if err != nil {
		return model.GeneratedMedia{}, err
	}
	return media, db.Save(&media).Error
}

// GetGeneratedMedia 获取指定用户拥有的生成图片。
func GetGeneratedMedia(userID string, id string) (model.GeneratedMedia, error) {
	db, err := DB()
	if err != nil {
		return model.GeneratedMedia{}, err
	}
	var media model.GeneratedMedia
	err = db.Where("user_id = ? AND id = ?", strings.TrimSpace(userID), strings.TrimSpace(id)).First(&media).Error
	return media, err
}

// ListGeneratedMediaByIDs 批量获取图片状态，供历史列表刷新本地文件状态。
func ListGeneratedMediaByIDs(userID string, ids []string) ([]model.GeneratedMedia, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	ids = generatedMediaIDs(ids)
	if len(ids) == 0 {
		return []model.GeneratedMedia{}, nil
	}
	var media []model.GeneratedMedia
	err = db.Where("user_id = ? AND id IN ?", strings.TrimSpace(userID), ids).Find(&media).Error
	return media, err
}

// ListExpiredLocalGeneratedMedia 查询超过本地保留期且尚未上传云端的图片。
func ListExpiredLocalGeneratedMedia(before string, limit int) ([]model.GeneratedMedia, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	var media []model.GeneratedMedia
	err = db.Where("storage_status = ? AND created_at < ?", model.GeneratedMediaStatusLocal, before).Order("created_at ASC").Limit(limit).Find(&media).Error
	return media, err
}

// DeleteGeneratedMedia 删除生成图片元数据。
func DeleteGeneratedMedia(userID string, id string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return db.Where("user_id = ? AND id = ?", strings.TrimSpace(userID), strings.TrimSpace(id)).Delete(&model.GeneratedMedia{}).Error
}

func generatedMediaIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
