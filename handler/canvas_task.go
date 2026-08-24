package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/service"
)

// canvasImageTaskRuns 只管理当前进程中的请求取消函数。删除数据库记录负责丢弃结果，取消 context 负责尽快断开仍在等待的上游请求。
var canvasImageTaskRuns sync.Map

type canvasImageTaskRun struct {
	cancel context.CancelFunc
}

func CreateCanvasImageTask(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	body, contentType, endpoint, source, nodeID, sourceID, clientTaskID, prompt, channelID, err := readCanvasTaskAIRequest(r, "/images/generations")
	if err != nil {
		Fail(w, err.Error())
		return
	}
	modelName := readAIModelFromBody(body, contentType)
	if strings.TrimSpace(modelName) == "" {
		Fail(w, "缺少模型名称")
		return
	}
	channelID = firstNonEmpty(channelID, r.Header.Get("X-Model-Channel-ID"))
	userChannelID := r.Header.Get(userModelChannelHeader)
	if strings.TrimSpace(channelID) == "" && strings.TrimSpace(userChannelID) == "" {
		Fail(w, "缺少模型渠道")
		return
	}
	channel, resolvedUserChannelID, err := selectAIRequestChannel(user, modelName, channelID, userChannelID)
	if err != nil {
		log.Printf("canvas image task select channel failed: model=%s err=%v", modelName, err)
		failAIChannelSelect(w, err, "AI 接口请求失败")
		return
	}
	task, err := service.CreateCanvasImageTask(service.CanvasImageTaskCreateInput{
		UserID:          user.ID,
		UserDisplayName: firstNonEmpty(user.DisplayName, user.Username),
		Source:          source,
		SourceID:        sourceID,
		NodeID:          nodeID,
		ClientTaskID:    clientTaskID,
		Model:           modelName,
		ChannelID:       channel.ID,
		UserChannelID:   resolvedUserChannelID,
		ChannelName:     channel.Name,
		Prompt:          prompt,
		GenerationType:  strings.TrimPrefix(endpoint, "/images/"),
		Endpoint:        endpoint,
		ContentType:     contentType,
		RequestBody:     summarizeAIRequest(body, contentType),
	})
	if err != nil {
		log.Printf("create canvas image task failed: user=%s err=%v", user.ID, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	taskContext, taskRun := registerCanvasImageTaskRun(r.Context(), user.ID, task.ID)
	OK(w, service.CanvasImageTaskResponse(task))
	go runCanvasImageTask(taskContext, taskRun, task, user, body, contentType, task.ChannelID, task.UserChannelID)
}

func GetCanvasImageTask(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	task, found, err := service.GetUserCanvasImageTask(user.ID, id)
	if err != nil {
		log.Printf("read canvas image task failed: user=%s id=%s err=%v", user.ID, id, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	if !found {
		Fail(w, "图片任务不存在")
		return
	}
	OK(w, service.CanvasImageTaskResponse(task))
}

func UserCanvasImageTasks(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	tasks, err := service.ListUserCanvasImageTasks(user.ID, readCanvasTaskSources(r), 100)
	if err != nil {
		log.Printf("list canvas image tasks failed: user=%s err=%v", user.ID, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	OK(w, tasks)
}

func BatchCanvasImageTasks(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	var request struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		Fail(w, "图片任务参数无效")
		return
	}
	tasks, err := service.BatchUserCanvasImageTasks(user.ID, request.IDs)
	if err != nil {
		log.Printf("batch canvas image tasks failed: user=%s err=%v", user.ID, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	OK(w, tasks)
}

func DeleteUserCanvasImageTask(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	if strings.TrimSpace(id) == "" {
		Fail(w, "图片任务不存在")
		return
	}
	cancelCanvasImageTaskRun(user.ID, id)
	if err := service.DeleteUserCanvasImageTask(user.ID, id); err != nil {
		log.Printf("delete canvas image task failed: user=%s id=%s err=%v", user.ID, id, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	OK(w, map[string]any{"deleted": true})
}

func DeleteUserCanvasTasks(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}

	var request struct {
		SourceID string   `json:"source_id"`
		NodeIDs  []string `json:"node_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.SourceID) == "" {
		Fail(w, "画布任务参数无效")
		return
	}

	if err := service.DeleteUserCanvasTasks(user.ID, request.SourceID, request.NodeIDs); err != nil {
		log.Printf("delete canvas tasks failed: user=%s source=%s err=%v", user.ID, request.SourceID, err)
		Fail(w, "AI 接口请求失败")
		return
	}

	OK(w, map[string]any{"deleted": true})
}

func CreateCanvasAudioTask(w http.ResponseWriter, r *http.Request) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	body, contentType, endpoint, _, nodeID, sourceID, clientTaskID, prompt, channelID, err := readCanvasTaskAIRequest(r, "/audio/speech")
	if err != nil {
		Fail(w, err.Error())
		return
	}
	modelName := readAIModelFromBody(body, contentType)
	if strings.TrimSpace(modelName) == "" {
		Fail(w, "缺少模型名称")
		return
	}
	channelID = firstNonEmpty(channelID, r.Header.Get("X-Model-Channel-ID"))
	userChannelID := r.Header.Get(userModelChannelHeader)
	if strings.TrimSpace(channelID) == "" && strings.TrimSpace(userChannelID) == "" {
		Fail(w, "缺少模型渠道")
		return
	}
	channel, resolvedUserChannelID, err := selectAIRequestChannel(user, modelName, channelID, userChannelID)
	if err != nil {
		log.Printf("canvas audio task select channel failed: model=%s err=%v", modelName, err)
		failAIChannelSelect(w, err, "AI 接口请求失败")
		return
	}
	task, err := service.CreateCanvasAudioTask(service.CanvasAudioTaskCreateInput{
		UserID:          user.ID,
		UserDisplayName: firstNonEmpty(user.DisplayName, user.Username),
		SourceID:        sourceID,
		NodeID:          nodeID,
		ClientTaskID:    clientTaskID,
		Model:           modelName,
		ChannelID:       channel.ID,
		UserChannelID:   resolvedUserChannelID,
		ChannelName:     channel.Name,
		Prompt:          prompt,
		Endpoint:        endpoint,
		ContentType:     contentType,
		RequestBody:     summarizeAIRequest(body, contentType),
	})
	if err != nil {
		log.Printf("create canvas audio task failed: user=%s err=%v", user.ID, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	OK(w, service.CanvasAudioTaskResponse(task))
	go runCanvasAudioTask(task, user, body, contentType, task.ChannelID, task.UserChannelID)
}

func GetCanvasAudioTask(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	task, found, err := service.GetUserCanvasAudioTask(user.ID, id)
	if err != nil {
		log.Printf("read canvas audio task failed: user=%s id=%s err=%v", user.ID, id, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	if !found {
		Fail(w, "音频任务不存在")
		return
	}
	OK(w, service.CanvasAudioTaskResponse(task))
}

func runCanvasImageTask(taskContext context.Context, taskRun *canvasImageTaskRun, task model.CanvasImageTask, user model.AuthUser, body []byte, contentType string, channelID string, userChannelID string) {
	defer finishCanvasImageTaskRun(user.ID, task.ID, taskRun)
	if taskContext.Err() != nil {
		return
	}
	current := taskTime()
	task.Status = "processing"
	task.Progress = 10
	task.StartedAt = current
	task, _ = service.SaveCanvasImageTask(task)

	payload, status, responseContentType, err := executeCanvasAIRequest(taskContext, user, task.Endpoint, body, contentType, channelID, userChannelID)
	if taskContext.Err() != nil {
		return
	}
	if err != nil {
		saveFailedCanvasImageTask(task, err.Error(), err.Error())
		return
	}
	if status >= http.StatusBadRequest {
		message := readUpstreamAIErrorMessage(payload, status)
		saveFailedCanvasImageTask(task, message, message)
		return
	}
	if message := readWrappedTaskError(payload); message != "" {
		saveFailedCanvasImageTask(task, message, message)
		return
	}
	collectAll := isKIESeedreamLayerDecompositionModel(task.Model)
	result, err := storeCanvasImageTaskResult(
		taskContext,
		user,
		payload,
		responseContentType,
		collectAll,
		task.Endpoint == "/chat/completions",
	)
	if taskContext.Err() != nil {
		return
	}
	if err != nil {
		saveFailedCanvasImageTask(task, err.Error(), err.Error())
		return
	}
	task.Status = "completed"
	task.Progress = 100
	task.CompletedAt = taskTime()
	task.ResponseBody = canvasImageTaskResponseBody(result.URLs)
	task.ImageURL = result.URLs[0]
	if collectAll {
		task.ImageURLs = result.URLs
	}
	task.StorageKey = result.StorageKey
	task.MimeType = result.MimeType
	task.Bytes = result.Bytes
	task.Width = result.Width
	task.Height = result.Height
	task.Error = ""
	task.ErrorDetail = ""
	_, _ = service.SaveCanvasImageTask(task)
}

func runCanvasAudioTask(task model.CanvasAudioTask, user model.AuthUser, body []byte, contentType string, channelID string, userChannelID string) {
	current := taskTime()
	task.Status = "processing"
	task.Progress = 10
	task.StartedAt = current
	task, _ = service.SaveCanvasAudioTask(task)

	payload, status, responseContentType, err := executeCanvasAIRequest(context.Background(), user, task.Endpoint, body, contentType, channelID, userChannelID)
	if err != nil {
		saveFailedCanvasAudioTask(task, err.Error(), err.Error())
		return
	}
	if status >= http.StatusBadRequest {
		message := readUpstreamAIErrorMessage(payload, status)
		saveFailedCanvasAudioTask(task, message, string(payload))
		return
	}
	if message := readWrappedTaskError(payload); message != "" {
		saveFailedCanvasAudioTask(task, message, string(payload))
		return
	}
	mimeType := strings.TrimSpace(strings.Split(responseContentType, ";")[0])
	if mimeType == "" {
		mimeType = strings.TrimSpace(http.DetectContentType(payload))
	}
	if strings.Contains(mimeType, "json") {
		saveFailedCanvasAudioTask(task, "音频接口没有返回音频文件", string(payload))
		return
	}
	if task.ContentType != "" && strings.HasPrefix(task.ContentType, "audio/") {
		mimeType = task.ContentType
	}
	task.Status = "completed"
	task.Progress = 100
	task.CompletedAt = taskTime()
	task.ResponseBody = "[binary audio]"
	task.AudioURL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(payload)
	task.StorageKey = ""
	task.MimeType = mimeType
	task.Bytes = int64(len(payload))
	task.Error = ""
	task.ErrorDetail = ""
	_, _ = service.SaveCanvasAudioTask(task)
}

func executeCanvasAIRequest(requestContext context.Context, user model.AuthUser, endpoint string, body []byte, contentType string, channelID string, userChannelID string) ([]byte, int, string, error) {
	request := httptest.NewRequest(http.MethodPost, "http://canvas.local/api/v1"+endpoint, bytes.NewReader(body))
	request = request.WithContext(service.WithUser(requestContext, user))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if strings.TrimSpace(userChannelID) != "" {
		request.Header.Set(userModelChannelHeader, userChannelID)
	} else if strings.TrimSpace(channelID) != "" {
		request.Header.Set("X-Model-Channel-ID", channelID)
	}
	recorder := httptest.NewRecorder()
	proxyAIRequest(recorder, request, endpoint)
	response := recorder.Result()
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
	return payload, response.StatusCode, response.Header.Get("Content-Type"), nil
}

func registerCanvasImageTaskRun(parent context.Context, userID string, taskID string) (context.Context, *canvasImageTaskRun) {
	// 创建接口返回后任务仍需继续执行，但必须保留请求 Origin，才能把本机图片写成完整可访问地址。
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	run := &canvasImageTaskRun{cancel: cancel}
	canvasImageTaskRuns.Store(canvasImageTaskRunKey(userID, taskID), run)
	return ctx, run
}

func cancelCanvasImageTaskRun(userID string, taskID string) {
	value, ok := canvasImageTaskRuns.LoadAndDelete(canvasImageTaskRunKey(userID, taskID))
	if !ok {
		return
	}
	value.(*canvasImageTaskRun).cancel()
}

func finishCanvasImageTaskRun(userID string, taskID string, run *canvasImageTaskRun) {
	run.cancel()
	key := canvasImageTaskRunKey(userID, taskID)
	if current, ok := canvasImageTaskRuns.Load(key); ok && current == run {
		canvasImageTaskRuns.Delete(key)
	}
}

func canvasImageTaskRunKey(userID string, taskID string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(taskID)
}

func saveFailedCanvasImageTask(task model.CanvasImageTask, message string, detail string) {
	task.Status = "failed"
	task.CompletedAt = taskTime()
	task.Error = firstNonEmpty(message, "图片生成失败")
	task.ErrorDetail = detail
	_, _ = service.SaveCanvasImageTask(task)
}

func saveFailedCanvasAudioTask(task model.CanvasAudioTask, message string, detail string) {
	task.Status = "failed"
	task.CompletedAt = taskTime()
	task.Error = firstNonEmpty(message, "音频生成失败")
	task.ErrorDetail = detail
	_, _ = service.SaveCanvasAudioTask(task)
}

func readCanvasTaskAIRequest(r *http.Request, fallbackEndpoint string) ([]byte, string, string, string, string, string, string, string, string, error) {
	contentType := r.Header.Get("Content-Type")
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", "", "", "", "", "", "", "", err
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		body, cleanedContentType, meta, err := stripCanvasTaskMultipartFields(raw, contentType, strings.HasPrefix(fallbackEndpoint, "/images/"))
		if err != nil {
			return nil, "", "", "", "", "", "", "", "", err
		}
		endpoint := firstNonEmpty(meta["_canvas_endpoint"], fallbackEndpoint)
		return body, cleanedContentType, endpoint, meta["_canvas_source"], meta["_canvas_node_id"], meta["_canvas_source_id"], meta["_canvas_task_id"], meta["_canvas_prompt"], meta["_canvas_channel_id"], nil
	}
	var wrapper struct {
		Endpoint     string          `json:"endpoint"`
		Source       string          `json:"source"`
		NodeID       string          `json:"nodeId"`
		SourceID     string          `json:"sourceId"`
		ClientTaskID string          `json:"clientTaskId"`
		TaskID       string          `json:"taskId"`
		Prompt       string          `json:"prompt"`
		ChannelID    string          `json:"channelId"`
		RequestBody  json.RawMessage `json:"requestBody"`
		Request      json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, "", "", "", "", "", "", "", "", err
	}
	body := wrapper.RequestBody
	if len(body) == 0 {
		body = wrapper.Request
	}
	if len(body) == 0 {
		return nil, "", "", "", "", "", "", "", "", errors.New("任务请求体不能为空")
	}
	endpoint := firstNonEmpty(wrapper.Endpoint, fallbackEndpoint)
	return body, "application/json", endpoint, wrapper.Source, wrapper.NodeID, wrapper.SourceID, firstNonEmpty(wrapper.ClientTaskID, wrapper.TaskID), wrapper.Prompt, wrapper.ChannelID, nil
}

func stripCanvasTaskMultipartFields(raw []byte, contentType string, normalizeImages bool) ([]byte, string, map[string]string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", nil, err
	}
	form, err := multipart.NewReader(bytes.NewReader(raw), params["boundary"]).ReadForm(256 << 20)
	if err != nil {
		return nil, "", nil, err
	}
	defer form.RemoveAll()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	meta := map[string]string{}
	for key, values := range form.Value {
		if strings.HasPrefix(key, "_canvas_") {
			if len(values) > 0 {
				meta[key] = values[0]
			}
			continue
		}
		for _, value := range values {
			_ = writer.WriteField(key, value)
		}
	}
	for key, files := range form.File {
		for _, header := range files {
			if err := writeCanvasTaskMultipartFile(writer, key, header, normalizeImages); err != nil {
				_ = writer.Close()
				return nil, "", nil, err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", nil, err
	}
	return buffer.Bytes(), writer.FormDataContentType(), meta, nil
}

func writeCanvasTaskMultipartFile(writer *multipart.Writer, field string, header *multipart.FileHeader, normalizeImage bool) error {
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	reader := io.Reader(file)
	mimeType, extension := "", ""
	if normalizeImage {
		buffered := bufio.NewReaderSize(file, 512)
		head, _ := buffered.Peek(512)
		mimeType, extension = canvasTaskImageType(head)
		reader = buffered
	}
	var part io.Writer
	if mimeType == "" {
		part, err = writer.CreateFormFile(field, header.Filename)
	} else {
		filename := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
		if filename == "" {
			filename = "reference"
		}
		partHeader := make(textproto.MIMEHeader)
		partHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": filename + extension}))
		partHeader.Set("Content-Type", mimeType)
		part, err = writer.CreatePart(partHeader)
	}
	if err != nil {
		return err
	}
	_, err = io.Copy(part, reader)
	return err
}

func canvasTaskImageType(data []byte) (string, string) {
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return "image/jpeg", ".jpg"
	case "image/png":
		return "image/png", ".png"
	case "image/webp":
		return "image/webp", ".webp"
	default:
		return "", ""
	}
}

func readAIModelFromBody(body []byte, contentType string) string {
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return readMultipartModel(body, contentType)
	}
	var payload struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &payload)
	return strings.TrimSpace(payload.Model)
}

func readWrappedTaskError(payload []byte) string {
	var root struct {
		Code  *int   `json:"code"`
		Msg   string `json:"msg"`
		Error any    `json:"error"`
		Data  any    `json:"data"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &root) != nil {
		return ""
	}
	if root.Code != nil && *root.Code != 0 {
		return firstNonEmpty(root.Msg, "AI 接口请求失败")
	}
	if root.Error != nil {
		if errMap, ok := root.Error.(map[string]any); ok {
			return firstNonEmpty(toStringSafe(errMap["message"]), toStringSafe(errMap["msg"]), toStringSafe(root.Error))
		}
		return toStringSafe(root.Error)
	}
	return ""
}

func imageBytesFromAIResponse(payload []byte) ([]byte, string, error) {
	candidates, err := imageCandidatesFromAIResponse(payload, "", false)
	if err != nil {
		return nil, "", err
	}
	for _, candidate := range candidates {
		data, mimeType, err := imageCandidateBytes(candidate)
		if err == nil && len(data) > 0 {
			return data, mimeType, nil
		}
	}
	return nil, "", errors.New("图片接口没有返回图片")
}

// canvasImageTaskResult 只保存图片位置和元数据，禁止携带图片字节或 Base64。
type canvasImageTaskResult struct {
	// URLs 是完整的本机内容接口地址或上游/对象存储 URL。
	URLs []string
	// StorageKey 标识第一张落到本机生成图片目录的图片。
	StorageKey string
	// MimeType 是第一张本机图片的媒体类型，远程 URL 未下载时为空。
	MimeType string
	// Bytes 是第一张本机图片的字节数，只保存数值。
	Bytes int64
	// Width 是第一张本机图片的像素宽度。
	Width int
	// Height 是第一张本机图片的像素高度。
	Height int
}

func storeCanvasImageTaskResult(
	ctx context.Context,
	user model.AuthUser,
	payload []byte,
	contentType string,
	collectAll bool,
	includeChatImages bool,
) (canvasImageTaskResult, error) {
	candidates, err := imageCandidatesFromAIResponse(payload, contentType, includeChatImages)
	if err != nil {
		return canvasImageTaskResult{}, err
	}
	result := canvasImageTaskResult{URLs: make([]string, 0, len(candidates))}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if ctx.Err() != nil {
			return canvasImageTaskResult{}, ctx.Err()
		}
		if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
			result.URLs = append(result.URLs, candidate)
		} else {
			data, mimeType, err := imageCandidateBytes(candidate)
			if err != nil || len(data) == 0 {
				continue
			}
			media, err := storeCanvasImageCandidate(ctx, user, data, mimeType)
			if err != nil {
				return canvasImageTaskResult{}, err
			}
			result.URLs = append(result.URLs, media.URL)
			if len(result.URLs) == 1 {
				result.StorageKey = media.StorageKey
				result.MimeType = media.MimeType
				result.Bytes = media.Bytes
				result.Width = media.Width
				result.Height = media.Height
			}
		}
		if !collectAll {
			return result, nil
		}
	}
	if len(result.URLs) == 0 {
		return canvasImageTaskResult{}, errors.New("图片接口没有返回图片")
	}
	return result, nil
}

func storeCanvasImageCandidate(ctx context.Context, user model.AuthUser, data []byte, mimeType string) (service.GeneratedMediaView, error) {
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		mimeType = strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return service.GeneratedMediaView{}, errors.New("图片接口返回了无效图片类型")
	}
	width, height := imageSize(data)
	// 图片字节只写入生成图片目录；任务表始终引用内容接口 URL，绝不保存 data URL。
	return service.CreateGeneratedMedia(service.WithUser(ctx, user), "canvas-image", mimeType, data, width, height, false, nil)
}

func canvasImageTaskResponseBody(urls []string) string {
	data := make([]map[string]string, 0, len(urls))
	for _, url := range urls {
		data = append(data, map[string]string{"url": url})
	}
	encoded, _ := json.Marshal(map[string]any{"data": data})
	return string(encoded)
}

type serverSentJSONEvent struct {
	name string
	data any
}

func imageCandidatesFromAIResponse(payload []byte, contentType string, includeChatImages bool) ([]string, error) {
	if !isServerSentEventResponse(payload, contentType) {
		var root any
		if err := json.Unmarshal(payload, &root); err != nil {
			return nil, err
		}
		return collectImageCandidates(root, 0, includeChatImages), nil
	}

	events, err := parseServerSentJSONEvents(payload)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		encoded, _ := json.Marshal(event.data)
		if message := readWrappedTaskError(encoded); message != "" {
			return nil, errors.New(message)
		}
		if strings.EqualFold(event.name, "error") {
			return nil, errors.New("图片流式接口返回错误")
		}
		candidates = append(candidates, collectImageCandidates(event.data, 0, includeChatImages)...)
	}
	return candidates, nil
}

func isServerSentEventResponse(payload []byte, contentType string) bool {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return true
	}
	trimmed := bytes.TrimSpace(payload)
	return bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:"))
}

func parseServerSentJSONEvents(payload []byte) ([]serverSentJSONEvent, error) {
	normalized := strings.ReplaceAll(string(payload), "\r\n", "\n")
	events := make([]serverSentJSONEvent, 0)
	for _, block := range strings.Split(normalized, "\n\n") {
		name := ""
		dataLines := make([]string, 0)
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(data), &decoded); err != nil {
			return nil, err
		}
		events = append(events, serverSentJSONEvent{name: name, data: decoded})
	}
	if len(events) == 0 {
		return nil, errors.New("图片流式接口没有返回可解析事件")
	}
	return events, nil
}

func collectImageCandidates(value any, depth int, includeChatImages bool) []string {
	if depth > 7 || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "data:image/") || looksLikeBase64(text) {
			return []string{text}
		}
	case []any:
		var result []string
		for _, item := range typed {
			result = append(result, collectImageCandidates(item, depth+1, includeChatImages)...)
		}
		return result
	case map[string]any:
		keys := []string{"url", "b64_json", "partial_image_b64", "image_url", "image", "image_data", "base64", "inlineData", "parts", "content", "candidates", "result", "response", "data", "output"}
		if includeChatImages {
			keys = append(keys, "choices", "message", "images")
		}
		var result []string
		for _, key := range keys {
			result = append(result, collectImageCandidates(typed[key], depth+1, includeChatImages)...)
		}
		return result
	}
	return nil
}

func imageCandidateBytes(value string) ([]byte, string, error) {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		response, err := http.Get(value)
		if err != nil {
			return nil, "", err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, "", errors.New(response.Status)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
		if err != nil {
			return nil, "", err
		}
		mimeType := response.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		return data, strings.Split(mimeType, ";")[0], nil
	}
	if strings.HasPrefix(value, "data:image/") {
		parts := strings.SplitN(value, ",", 2)
		if len(parts) != 2 {
			return nil, "", errors.New("无效图片 data url")
		}
		mimeType := strings.TrimPrefix(strings.Split(strings.TrimPrefix(parts[0], "data:"), ";")[0], " ")
		data, err := base64.StdEncoding.DecodeString(parts[1])
		return data, mimeType, err
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", err
	}
	return data, http.DetectContentType(data), nil
}

func imageSize(data []byte) (int, int) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

func taskTime() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func readCanvasTaskSources(r *http.Request) []string {
	values := r.URL.Query()["source"]
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if strings.TrimSpace(item) != "" {
				result = append(result, strings.TrimSpace(item))
			}
		}
	}
	return result
}
