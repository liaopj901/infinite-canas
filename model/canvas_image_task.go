package model

// CanvasImageTask 保存画布图片生成任务及其结果元数据。
type CanvasImageTask struct {
	// ID 是任务唯一标识。
	ID string `json:"id" gorm:"primaryKey"`
	// UserID 是任务所属用户 ID。
	UserID string `json:"userId" gorm:"index:idx_canvas_image_tasks_user_source_node,priority:1;index:idx_canvas_image_tasks_user_deleted_created,priority:1"`
	// UserDisplayName 是任务创建时的用户展示名称。
	UserDisplayName string `json:"userDisplayName"`
	// Source 是任务来源，例如 canvas 或 image-workbench。
	Source string `json:"source" gorm:"index:idx_canvas_image_tasks_user_source_node,priority:2"`
	// SourceID 是来源画布或工作台的标识。
	SourceID string `json:"sourceId" gorm:"index:idx_canvas_image_tasks_user_source_node,priority:3"`
	// NodeID 是来源画布节点 ID。
	NodeID string `json:"nodeId" gorm:"index:idx_canvas_image_tasks_user_source_node,priority:4"`
	// Model 是生成任务使用的模型名称。
	Model string `json:"model"`
	// ChannelID 是系统渠道 ID。
	ChannelID string `json:"channelId"`
	// UserChannelID 是用户自定义渠道 ID。
	UserChannelID string `json:"userChannelId"`
	// ChannelName 是任务创建时的渠道名称。
	ChannelName string `json:"channelName"`
	// Status 是任务当前状态。
	Status string `json:"status"`
	// Progress 是任务进度百分比。
	Progress int `json:"progress"`
	// Prompt 是图片生成提示词。
	Prompt string `json:"prompt" gorm:"type:text"`
	// GenerationType 区分文生图和图片编辑任务。
	GenerationType string `json:"generationType"`
	// Endpoint 是上游请求端点。
	Endpoint string `json:"endpoint"`
	// ContentType 是上游请求内容类型。
	ContentType string `json:"contentType"`
	// RequestBody 保存精简后的请求信息，不应包含图片字节。
	RequestBody string `json:"requestBody" gorm:"type:text"`
	// ResponseBody 保存精简后的响应信息，不应包含图片字节。
	ResponseBody string `json:"responseBody" gorm:"type:text"`
	// Error 是失败摘要。
	Error string `json:"error" gorm:"type:text"`
	// ErrorDetail 是失败详情。
	ErrorDetail string `json:"errorDetail" gorm:"type:text"`
	// ImageURL 是第一张生成图片的本机或云端 URL。
	ImageURL string `json:"imageUrl" gorm:"type:text"`
	// ImageURLs 是全部生成图片的本机或云端 URL。
	ImageURLs []string `json:"imageUrls" gorm:"serializer:json"`
	// StorageKey 是第一张图片的存储对象标识，使用 local: 或 server: 前缀区分存储状态。
	StorageKey string `json:"storageKey"`
	// Width 是第一张图片的像素宽度。
	Width int `json:"width"`
	// Height 是第一张图片的像素高度。
	Height int `json:"height"`
	// MimeType 是第一张图片的媒体类型。
	MimeType string `json:"mimeType"`
	// Bytes 是第一张图片的字节数值，不包含图片内容。
	Bytes int64 `json:"bytes"`
	// CreatedAt 是任务创建时间。
	CreatedAt string `json:"createdAt" gorm:"index:idx_canvas_image_tasks_user_deleted_created,priority:3"`
	// UpdatedAt 是任务最后更新时间。
	UpdatedAt string `json:"updatedAt"`
	// StartedAt 是任务开始处理时间。
	StartedAt string `json:"startedAt"`
	// CompletedAt 是任务完成时间。
	CompletedAt string `json:"completedAt"`
	// DeletedAt 是软删除时间，空字符串表示任务仍可见。
	DeletedAt string `json:"deletedAt" gorm:"not null;default:'';index:idx_canvas_image_tasks_user_deleted_created,priority:2"`
}
