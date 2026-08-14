package model

const (
	// GeneratedMediaStatusLocal 表示图片仅保存在应用服务器本地。
	GeneratedMediaStatusLocal = "local"
	// GeneratedMediaStatusCloud 表示图片已上传云端，本地副本可以删除。
	GeneratedMediaStatusCloud = "cloud"
	// GeneratedMediaStatusCleaned 表示本地图片已按保留策略清理。
	GeneratedMediaStatusCleaned = "cleaned"
)

// GeneratedMedia 记录生成图片的本地文件和云端对象关系，不在数据库保存图片二进制。
type GeneratedMedia struct {
	// ID 是生成图片的稳定标识，同时用于构造 local: 存储键。
	ID string `json:"id" gorm:"primaryKey"`
	// UserID 是图片所有者，用于隔离不同用户的本地文件访问。
	UserID string `json:"userId" gorm:"index;index:idx_generated_media_user_status_created,priority:1"`
	// RelativePath 是相对 GENERATED_MEDIA_DIR 的文件路径，禁止保存绝对路径。
	RelativePath string `json:"relativePath"`
	// FileName 是上传时的原始文件名，仅用于下载和云端对象扩展名。
	FileName string `json:"fileName"`
	// MimeType 是图片的媒体类型。
	MimeType string `json:"mimeType"`
	// Bytes 是图片字节数。
	Bytes int64 `json:"bytes"`
	// Width 是图片像素宽度。
	Width int `json:"width"`
	// Height 是图片像素高度。
	Height int `json:"height"`
	// StorageObjectID 是上传云端后关联的 storage_objects 主键。
	StorageObjectID string `json:"storageObjectId" gorm:"index"`
	// CloudURL 是云端公开地址或受保护内容接口地址。
	CloudURL string `json:"cloudUrl"`
	// StorageStatus 表示图片当前位于本地、云端或已清理。
	StorageStatus string `json:"storageStatus" gorm:"index;index:idx_generated_media_user_status_created,priority:2"`
	// StorageMessage 记录不影响本地图片可用性的云端上传失败等提示。
	StorageMessage string `json:"storageMessage"`
	// CreatedAt 是记录创建时间，使用 RFC3339 字符串保持现有项目时间格式一致。
	CreatedAt string `json:"createdAt" gorm:"index;index:idx_generated_media_user_status_created,priority:3"`
	// UpdatedAt 是记录最后更新时间。
	UpdatedAt string `json:"updatedAt"`
	// CleanedAt 是本地文件被清理的时间，未清理时为空。
	CleanedAt string `json:"cleanedAt"`
}
