package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/service"
)

const userModelChannelHeader = "X-User-Model-Channel-ID"

const (
	imageUpstreamMaxRetries = 3
	imageUpstreamRetryDelay = 700 * time.Millisecond
)

func selectAIRequestChannel(user model.AuthUser, modelName string, channelID string, userChannelID string) (model.ModelChannel, string, error) {
	userChannelID = strings.TrimSpace(userChannelID)
	if userChannelID != "" {
		channel, err := service.SelectUserLocalModelChannelForModel(user.ID, modelName, userChannelID)
		return channel, userChannelID, err
	}
	if !service.UserCanUseRemoteModelChannel(user) {
		return model.ModelChannel{}, "", fmt.Errorf("当前账号未开放云端渠道")
	}
	channel, err := service.SelectModelChannelForModel(modelName, channelID)
	return channel, "", err
}

func failAIChannelSelect(w http.ResponseWriter, err error, fallback string) {
	message := strings.TrimSpace(err.Error())
	switch message {
	case "当前账号未开放云端渠道", "请先登录", "缺少模型名称", "缺少模型渠道", "本地渠道不存在", "本地渠道配置不完整", "本地渠道不支持该模型", "指定模型渠道不可用":
		Fail(w, message)
	default:
		Fail(w, fallback)
	}
}

func AIImagesGenerations(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/images/generations")
}

func AIImagesEdits(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/images/edits")
}

func AIChatCompletions(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/chat/completions")
}

func AIResponses(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/responses")
}

func AIVideos(w http.ResponseWriter, r *http.Request) {
	proxyAIVideoTaskRequest(w, r)
}

func AIVideo(w http.ResponseWriter, r *http.Request, id string) {
	if serveAIVideoTask(w, r, id) {
		return
	}
	if isClientVideoTaskID(id) {
		OK(w, map[string]any{"id": id, "task_id": id, "object": "video", "status": "queued", "progress": 0})
		return
	}
	proxyAIGetRequest(w, r, "/videos/"+id)
}

func AIVideoContent(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/videos/"+id+"/content")
}

func AIAudioSpeech(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/audio/speech")
}

func proxyAIGetRequest(w http.ResponseWriter, r *http.Request, path string) {
	startedAt := time.Now()
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	modelName := r.URL.Query().Get("model")
	if strings.TrimSpace(modelName) == "" {
		modelName = "Agnes-Video-V2.0"
	}
	channel, _, err := selectAIRequestChannel(user, modelName, r.Header.Get("X-Model-Channel-ID"), r.Header.Get(userModelChannelHeader))
	if err != nil {
		log.Printf("AI proxy select channel failed: model=%s err=%v", modelName, err)
		failAIChannelSelect(w, err, "AI 接口请求失败")
		return
	}
	upstreamPath := resolveAIProxyPath(channel, modelName, path)
	request, err := http.NewRequest(http.MethodGet, resolveAIProxyURL(channel, modelName, upstreamPath), nil)
	if err != nil {
		Fail(w, "AI 接口请求失败")
		return
	}
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	copyAIResponse(w, request, channel, aiLogContext{StartedAt: startedAt, Endpoint: path, Method: http.MethodGet, Model: modelName, Channel: channel, UserID: user.ID, UserDisplayName: firstNonEmpty(user.DisplayName, user.Username), RequestBody: summarizeQueryParams(r.URL.Query())}, nil)
}

func proxyAIRequest(w http.ResponseWriter, r *http.Request, path string) {
	startedAt := time.Now()
	body, contentType, modelName, err := readAIRequest(r)
	if err != nil {
		log.Printf("AI proxy request read failed: %v", err)
		Fail(w, "AI 接口请求失败")
		return
	}
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	channel, userChannelID, err := selectAIRequestChannel(user, modelName, r.Header.Get("X-Model-Channel-ID"), r.Header.Get(userModelChannelHeader))
	if err != nil {
		log.Printf("AI proxy select channel failed: model=%s err=%v", modelName, err)
		failAIChannelSelect(w, err, "AI 接口请求失败")
		return
	}
	credits := 0
	if userChannelID == "" {
		credits, err = service.ModelCost(modelName)
		if err != nil {
			log.Printf("AI proxy read model cost failed: model=%s err=%v", modelName, err)
			Fail(w, "AI 接口请求失败")
			return
		}
		credits *= readAIRequestCount(body, contentType)
	}
	upstreamPath := resolveAIProxyPath(channel, modelName, path)
	if service.IsMiMoTTSModelName(modelName) && path == "/audio/speech" {
		body, contentType, err = normalizeMiMoTTSBody(body, contentType, modelName)
		if err != nil {
			log.Printf("AI proxy normalize MiMo TTS request failed: model=%s err=%v", modelName, err)
			Fail(w, err.Error())
			return
		}
	} else if isKIEChannel(channel, modelName) && isKIECreateTaskPath(upstreamPath) {
		body, contentType, err = normalizeKIEVideoBody(body, contentType, modelName, channel)
		if err != nil {
			log.Printf("AI proxy normalize KIE request failed: model=%s err=%v", modelName, err)
			Fail(w, "AI 接口请求失败")
			return
		}
	} else if isAPIMartChannel(channel, modelName) && upstreamPath == "/videos/generations" {
		body, contentType, err = normalizeAPIMartVideoBody(body, contentType, modelName, channel)
		if err != nil {
			log.Printf("AI proxy normalize APIMart video request failed: model=%s err=%v", modelName, err)
			Fail(w, "AI 接口请求失败")
			return
		}
	} else if isAPIMartChannel(channel, modelName) && (upstreamPath == "/images/generations" || upstreamPath == "/images/edits") {
		body, contentType, err = normalizeAPIMartImageBody(body, contentType, modelName, channel)
		if err != nil {
			log.Printf("AI proxy normalize APIMart image request failed: model=%s err=%v", modelName, err)
			Fail(w, "AI 接口请求失败")
			return
		}
	}
	// 必须继承客户端 context；用户取消请求时才能终止服务端仍在等待的上游连接。
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, service.BuildModelChannelURL(channel, upstreamPath), bytes.NewReader(body))
	if err != nil {
		log.Printf("AI proxy build request failed: url=%s err=%v", service.BuildModelChannelURL(channel, upstreamPath), err)
		Fail(w, "AI 接口请求失败")
		return
	}
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if credits > 0 {
		if err := service.ConsumeUserCredits(user.ID, modelName, credits, upstreamPath); err != nil {
			FailError(w, err)
			return
		}
	}
	copyAIResponse(w, request, channel, aiLogContext{
		StartedAt:       startedAt,
		Endpoint:        path,
		Method:          http.MethodPost,
		Model:           modelName,
		Channel:         channel,
		UserID:          user.ID,
		UserDisplayName: firstNonEmpty(user.DisplayName, user.Username),
		Credits:         credits,
		RequestBody:     summarizeAIRequest(body, contentType),
		ImageRequest:    isImageAIRequest(path, body),
	}, func() {
		if credits > 0 {
			if err := service.RefundUserCredits(user.ID, modelName, credits, upstreamPath); err != nil {
				log.Printf("AI proxy refund credits failed: user=%s model=%s credits=%d err=%v", user.ID, modelName, credits, err)
			}
		}
	})
}

type aiLogContext struct {
	StartedAt       time.Time
	Endpoint        string
	Method          string
	Model           string
	Channel         model.ModelChannel
	UserID          string
	UserDisplayName string
	Credits         int
	RequestBody     string
	ImageRequest    bool
}

func copyAIResponse(w http.ResponseWriter, request *http.Request, channel model.ModelChannel, logContext aiLogContext, onFailure func()) {
	response, err := doAIRequestWithRetry(request, channel, logContext.ImageRequest)
	if err != nil {
		if handleCanceledAIRequest(request, onFailure) {
			return
		}
		log.Printf("AI proxy request failed: url=%s err=%v", request.URL.String(), err)
		if onFailure != nil {
			onFailure()
		}
		saveAIProxyLog(logContext, 0, "", err.Error())
		Fail(w, imageRetryFailureMessage(logContext.ImageRequest, 0, err))
		return
	}
	defer response.Body.Close()
	if handleCanceledAIRequest(request, onFailure) {
		return
	}

	if response.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 256*1024))
		if handleCanceledAIRequest(request, onFailure) {
			return
		}
		log.Printf("AI upstream error: url=%s status=%d body=%s", request.URL.String(), response.StatusCode, strings.TrimSpace(string(payload)))
		if onFailure != nil {
			onFailure()
		}
		saveAIProxyLog(logContext, response.StatusCode, string(payload), strings.TrimSpace(string(payload)))
		message := readUpstreamAIErrorMessage(payload, response.StatusCode)
		if shouldRetryImageUpstream(logContext.ImageRequest, response.StatusCode, payload) {
			message = imageRetryFailureMessage(logContext.ImageRequest, response.StatusCode, nil)
		}
		Fail(w, message)
		return
	}

	if copyMiMoTTSResponse(w, response, logContext, onFailure) {
		return
	}
	if copyKIEVideoResponse(w, response, request, channel, logContext, onFailure) {
		return
	}
	if isAPIMartChannel(channel, logContext.Model) {
		if copyAPIMartImageResponse(w, response, request, channel, logContext, onFailure) {
			return
		}
		if copyAPIMartVideoResponse(w, response, request, channel, logContext) {
			return
		}
	}

	for key, values := range response.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	responseBody := copyAIResponseBody(w, response.Body)
	if handleCanceledAIRequest(request, onFailure) {
		return
	}
	saveAIProxyLog(logContext, response.StatusCode, responseBody, "")
}

func handleCanceledAIRequest(request *http.Request, onFailure func()) bool {
	if request.Context().Err() == nil {
		return false
	}
	// 用户主动取消不应落成失败调用日志，但已扣除的站内额度必须按无结果请求退回。
	if onFailure != nil {
		onFailure()
	}
	return true
}

func doAIRequestWithRetry(request *http.Request, channel model.ModelChannel, retryImage bool) (*http.Response, error) {
	client := service.HTTPClientForChannel(channel)
	maxRetries := 0
	if retryImage {
		maxRetries = imageUpstreamMaxRetries
	}
	// 图片 POST 只有在没有明确业务错误时才重放，避免把余额不足或参数错误放大成重复任务。
	for attempt := 0; ; attempt++ {
		current, err := cloneAIRequest(request)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(current)
		if err != nil {
			if request.Context().Err() != nil {
				return nil, request.Context().Err()
			}
			if attempt >= maxRetries {
				if retryImage {
					return nil, &imageUpstreamRetryError{err: err}
				}
				return nil, err
			}
			log.Printf("AI upstream temporary failure, retrying: url=%s attempt=%d/%d err=%v", request.URL.String(), attempt+1, maxRetries, err)
			if err := waitAIRequestRetry(request, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if !shouldInspectImageRetryResponse(retryImage, response.StatusCode) {
			return response, nil
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, 256*1024))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if request.Context().Err() != nil {
			return nil, request.Context().Err()
		}
		response.Body = io.NopCloser(bytes.NewReader(payload))
		if !shouldRetryImageUpstream(retryImage, response.StatusCode, payload) || attempt >= maxRetries {
			return response, nil
		}
		log.Printf("AI upstream temporary failure, retrying: url=%s status=%d attempt=%d/%d", request.URL.String(), response.StatusCode, attempt+1, maxRetries)
		if err := waitAIRequestRetry(request, attempt); err != nil {
			return nil, err
		}
	}
}

type imageUpstreamRetryError struct {
	err error
}

func (e *imageUpstreamRetryError) Error() string {
	return e.err.Error()
}

func (e *imageUpstreamRetryError) Unwrap() error {
	return e.err
}

func cloneAIRequest(request *http.Request) (*http.Request, error) {
	cloned := request.Clone(request.Context())
	if request.Body == nil {
		return cloned, nil
	}
	if request.GetBody == nil {
		return nil, errors.New("AI 请求体无法重试")
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, err
	}
	cloned.Body = body
	return cloned, nil
}

func waitAIRequestRetry(request *http.Request, attempt int) error {
	// 线性退避避免连续冲击临时故障的上游，同时保持图片任务等待时间可控。
	timer := time.NewTimer(imageUpstreamRetryDelay * time.Duration(attempt+1))
	defer timer.Stop()
	select {
	case <-request.Context().Done():
		return request.Context().Err()
	case <-timer.C:
		return nil
	}
}

func isImageAIRequest(endpoint string, body []byte) bool {
	switch endpoint {
	case "/images/generations", "/images/edits":
		return true
	case "/responses":
		var payload struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if json.Unmarshal(body, &payload) != nil {
			return false
		}
		for _, tool := range payload.Tools {
			if tool.Type == "image_generation" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func shouldInspectImageRetryResponse(retryImage bool, statusCode int) bool {
	if !retryImage {
		return false
	}
	switch statusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func shouldRetryImageUpstream(retryImage bool, statusCode int, body []byte) bool {
	return shouldInspectImageRetryResponse(retryImage, statusCode) && readKnownUpstreamAIErrorMessage(body) == ""
}

func imageRetryFailureMessage(retryImage bool, statusCode int, err error) string {
	if !retryImage {
		return "AI 接口请求失败"
	}
	if statusCode > 0 {
		return fmt.Sprintf("上游临时不可用：%d，已重试 %d 次", statusCode, imageUpstreamMaxRetries)
	}
	var retryError *imageUpstreamRetryError
	if errors.As(err, &retryError) {
		return fmt.Sprintf("上游网络异常，已重试 %d 次", imageUpstreamMaxRetries)
	}
	return "AI 接口请求失败"
}

func copyAIResponseBody(w http.ResponseWriter, body io.Reader) string {
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	var logBuffer strings.Builder
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return logBuffer.String()
			}
			if logBuffer.Len() < 64*1024 {
				_, _ = logBuffer.Write(buffer[:min(n, 64*1024-logBuffer.Len())])
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return logBuffer.String()
		}
	}
}

func saveAIProxyLog(context aiLogContext, status int, responseBody string, errorMessage string) {
	if context.StartedAt.IsZero() {
		context.StartedAt = time.Now()
	}
	service.SaveAICallLog(service.AICallLogInput{
		UserID:          context.UserID,
		UserDisplayName: context.UserDisplayName,
		Endpoint:        context.Endpoint,
		Method:          context.Method,
		Model:           context.Model,
		ChannelID:       context.Channel.ID,
		ChannelName:     context.Channel.Name,
		Status:          status,
		DurationMs:      time.Since(context.StartedAt).Milliseconds(),
		Credits:         context.Credits,
		RequestBody:     context.RequestBody,
		ResponseBody:    responseBody,
		Error:           errorMessage,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func summarizeAIRequest(body []byte, contentType string) string {
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return summarizeMultipartAIRequest(body, contentType)
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err == nil {
		redactLargeImages(&payload)
		if encoded, err := json.MarshalIndent(payload, "", "  "); err == nil {
			return string(encoded)
		}
	}
	return string(body)
}

func summarizeQueryParams(values map[string][]string) string {
	if len(values) == 0 {
		return ""
	}
	encoded, _ := json.MarshalIndent(values, "", "  ")
	return string(encoded)
}

func summarizeMultipartAIRequest(body []byte, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "multipart/form-data"
	}
	form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(32 << 20)
	if err != nil {
		return "multipart/form-data"
	}
	defer form.RemoveAll()
	summary := map[string]any{"fields": form.Value}
	files := []map[string]any{}
	for field, headers := range form.File {
		for _, header := range headers {
			files = append(files, map[string]any{"field": field, "filename": header.Filename, "size": header.Size, "contentType": header.Header.Get("Content-Type")})
		}
	}
	summary["files"] = files
	encoded, _ := json.MarshalIndent(summary, "", "  ")
	return string(encoded)
}

func readUpstreamAIErrorMessage(body []byte, statusCode int) string {
	if message := readKnownUpstreamAIErrorMessage(body); message != "" {
		return message
	}
	if statusCode > 0 {
		return fmt.Sprintf("AI 接口请求失败：%d", statusCode)
	}
	return "AI 接口请求失败"
}

func readKnownUpstreamAIErrorMessage(body []byte) string {
	var payload struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if len(body) > 0 && json.Unmarshal(body, &payload) == nil {
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			return payload.Error.Message
		}
		if strings.TrimSpace(payload.Msg) != "" {
			return payload.Msg
		}
		if strings.TrimSpace(payload.Message) != "" {
			return payload.Message
		}
	}
	return ""
}

func redactLargeImages(value *any) {
	switch typed := (*value).(type) {
	case map[string]any:
		for key, item := range typed {
			if text, ok := item.(string); ok && (strings.HasPrefix(text, "data:image/") || strings.HasPrefix(text, "data:audio/") || len(text) > 2048 && looksLikeBase64(text)) {
				typed[key] = fmt.Sprintf("[redacted media/string len=%d]", len(text))
				continue
			}
			redactLargeImages(&item)
			typed[key] = item
		}
	case []any:
		for index, item := range typed {
			redactLargeImages(&item)
			typed[index] = item
		}
	}
}

func looksLikeBase64(value string) bool {
	for _, char := range value[:min(len(value), 200)] {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '+' || char == '/' || char == '=') {
			return false
		}
	}
	return true
}

func readAIRequest(r *http.Request) ([]byte, string, string, error) {
	contentType := r.Header.Get("Content-Type")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", "", err
	}
	modelName := ""
	if strings.HasPrefix(contentType, "multipart/form-data") {
		modelName = readMultipartModel(body, contentType)
	} else {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &payload)
		modelName = payload.Model
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, "", "", errMissingModel
	}
	return body, contentType, modelName, nil
}

func readMultipartModel(body []byte, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		return ""
	}
	defer form.RemoveAll()
	if values := form.Value["model"]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func readAIRequestCount(body []byte, contentType string) int {
	count := 1
	if strings.HasPrefix(contentType, "multipart/form-data") {
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return count
		}
		form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(32 << 20)
		if err != nil {
			return count
		}
		defer form.RemoveAll()
		if values := form.Value["n"]; len(values) > 0 {
			_, _ = fmt.Sscan(values[0], &count)
		}
	} else {
		var payload struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(body, &payload)
		count = payload.N
	}
	if count < 1 {
		return 1
	}
	return count
}

func resolveAIProxyURL(channel model.ModelChannel, modelName string, path string) string {
	if videoID, ok := agnesVideoQueryID(modelName, path); ok {
		baseURL := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
		if strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
			baseURL = strings.TrimRight(baseURL[:len(baseURL)-len("/v1")], "/")
		}
		values := url.Values{}
		values.Set("video_id", videoID)
		values.Set("model_name", modelName)
		return baseURL + "/agnesapi?" + values.Encode()
	}
	return service.BuildModelChannelURL(channel, path)
}

func agnesVideoQueryID(modelName string, path string) (string, bool) {
	if !isAgnesVideoModel(modelName) || !strings.HasPrefix(path, "/videos/") || strings.HasSuffix(path, "/content") {
		return "", false
	}
	id := strings.TrimPrefix(path, "/videos/")
	if strings.HasPrefix(id, "video_") {
		return id, true
	}
	return "", false
}

func resolveAIProxyPath(channel model.ModelChannel, modelName string, path string) string {
	if service.IsMiMoTTSModelName(modelName) && path == "/audio/speech" {
		return "/chat/completions"
	}
	if isCogVideoX3Model(modelName) {
		if path == "/videos" {
			return "/videos/generations"
		}
		if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
			taskID := strings.TrimSpace(strings.TrimPrefix(path, "/videos/"))
			if taskID != "" && !strings.Contains(taskID, "/") {
				return "/async-result/" + url.PathEscape(taskID)
			}
		}
		return path
	}
	if isKIEChannel(channel, modelName) {
		if path == "/images/generations" && strings.EqualFold(strings.TrimSpace(modelName), "grok-imagine-image-2-0/text-to-image") {
			return "/client/tasks"
		}
		if path == "/videos" || path == "/images/generations" || path == "/images/edits" {
			return "/jobs/createTask"
		}
		if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
			taskID := strings.TrimSpace(strings.TrimPrefix(path, "/videos/"))
			if taskID != "" && !strings.Contains(taskID, "/") {
				return "/jobs/recordInfo?taskId=" + url.QueryEscape(taskID)
			}
		}
		return path
	}
	if isAPIMartChannel(channel, modelName) {
		if path == "/videos" {
			return "/videos/generations"
		}
		if path == "/images/edits" {
			model := normalizeAPIMartModelName(modelName)
			if strings.Contains(model, "grok-imagine") && strings.Contains(model, "edit") {
				return path
			}
			return "/images/generations"
		}
		if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
			taskID := strings.TrimSpace(strings.TrimPrefix(path, "/videos/"))
			if taskID != "" && !strings.Contains(taskID, "/") {
				return "/tasks/" + url.PathEscape(taskID) + "?language=zh"
			}
		}
		return path
	}
	if strings.EqualFold(strings.TrimSpace(modelName), "grok-imagine-video") && path == "/videos" {
		return "/videos/generations"
	}
	if isArkSeedanceVideo(channel.BaseURL, modelName) {
		if path == "/videos" {
			return "/contents/generations/tasks"
		}
		if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
			return "/contents/generations/tasks/" + strings.TrimPrefix(path, "/videos/")
		}
	}
	return path
}

func isCogVideoX3Model(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), "cogvideox-3")
}

func isArkSeedanceVideo(baseURL string, modelName string) bool {
	base := strings.ToLower(baseURL)
	model := strings.ToLower(modelName)
	return strings.Contains(model, "seedance") || strings.Contains(model, "doubao-seedance") || strings.Contains(base, "/api/plan/v3")
}

func isAgnesVideoModel(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "agnes-video")
}

var errMissingModel = &aiError{"缺少模型名称"}

type aiError struct {
	message string
}

func (err *aiError) Error() string {
	return err.message
}
