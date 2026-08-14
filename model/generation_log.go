package model

type VideoGenerationLog struct {
	ID          string `json:"id" gorm:"primaryKey"`
	UserID      string `json:"userId" gorm:"index;index:idx_video_generation_logs_user_deleted_created,priority:1"`
	TaskID      string `json:"taskId" gorm:"index"`
	VideoID     string `json:"videoId" gorm:"index"`
	Status      string `json:"status" gorm:"index"`
	PayloadJSON string `json:"payloadJson" gorm:"type:text"`
	CreatedAt   string `json:"createdAt" gorm:"index;index:idx_video_generation_logs_user_deleted_created,priority:3"`
	UpdatedAt   string `json:"updatedAt" gorm:"index"`
	DeletedAt   string `json:"deletedAt" gorm:"index;index:idx_video_generation_logs_user_deleted_created,priority:2"`
}

// ImageGenerationLog 保存图片生成记录的完整详情和轻量列表摘要。
type ImageGenerationLog struct {
	// ID 是前端生成记录的稳定标识。
	ID string `json:"id" gorm:"primaryKey"`
	// UserID 是记录所有者。
	UserID string `json:"userId" gorm:"index;index:idx_image_generation_logs_user_deleted_created,priority:1"`
	// TaskID 是后端异步图片任务 ID。
	TaskID string `json:"taskId" gorm:"index"`
	// ImageID 是主图片或存储对象标识，用于去重。
	ImageID string `json:"imageId" gorm:"index"`
	// Status 是生成记录状态。
	Status string `json:"status" gorm:"index"`
	// PayloadJSON 保存完整详情，仅用于单条持久化，不参与列表查询。
	PayloadJSON string `json:"payloadJson" gorm:"type:text"`
	// SummaryJSON 只保存列表展示字段，避免返回完整任务、参考图和错误详情。
	SummaryJSON string `json:"summaryJson" gorm:"type:text"`
	// CreatedAt 是记录创建时间。
	CreatedAt string `json:"createdAt" gorm:"index;index:idx_image_generation_logs_user_deleted_created,priority:3"`
	// UpdatedAt 是记录最后更新时间。
	UpdatedAt string `json:"updatedAt" gorm:"index"`
	// DeletedAt 是软删除时间，未删除时为空。
	DeletedAt string `json:"deletedAt" gorm:"index;index:idx_image_generation_logs_user_deleted_created,priority:2"`
}
