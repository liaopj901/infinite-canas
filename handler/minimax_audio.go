package handler

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/service"
)

func prepareMiniMaxTTSRequest(input aiProtocolRequest) (aiProtocolRequest, bool, error) {
	official := service.IsMiniMaxChannel(input.channel) && service.IsMiniMaxTTSModelName(input.modelName)
	tokenDance := service.IsTokenDanceChannel(input.channel) && input.protocol == "minimax:t2a_v2"
	if input.endpoint != "/audio/speech" || !official && !tokenDance {
		return input, false, nil
	}
	if !strings.Contains(strings.ToLower(input.contentType), "application/json") {
		return input, true, errors.New("MiniMax TTS 仅支持 JSON 请求")
	}
	var request struct {
		Input          string  `json:"input"`
		Voice          string  `json:"voice"`
		ResponseFormat string  `json:"response_format"`
		Speed          float64 `json:"speed"`
	}
	if err := json.Unmarshal(input.body, &request); err != nil {
		return input, true, err
	}
	request.Input = strings.TrimSpace(request.Input)
	if request.Input == "" {
		return input, true, errors.New("MiniMax TTS 缺少播报文本")
	}
	request.Voice = strings.TrimSpace(request.Voice)
	if request.Voice == "" {
		request.Voice = "female-yujie"
	}
	request.ResponseFormat = strings.ToLower(strings.TrimSpace(request.ResponseFormat))
	if request.ResponseFormat != "wav" && request.ResponseFormat != "flac" {
		request.ResponseFormat = "mp3"
	}
	if request.Speed < 0.5 || request.Speed > 2 {
		request.Speed = 1
	}
	body, err := json.Marshal(map[string]any{
		"model":         strings.TrimSpace(input.modelName),
		"text":          request.Input,
		"stream":        false,
		"output_format": "hex",
		"voice_setting": map[string]any{
			"voice_id": request.Voice,
			"speed":    request.Speed,
			"vol":      1,
			"pitch":    0,
		},
		"audio_setting": map[string]any{
			"sample_rate": 32000,
			"bitrate":     128000,
			"format":      request.ResponseFormat,
			"channel":     1,
		},
	})
	if err != nil {
		return input, true, err
	}
	input.failureLabel = "MiniMax TTS"
	input.body = body
	input.contentType = "application/json"
	return input, true, nil
}

func copyMiniMaxTTSResponse(w http.ResponseWriter, response *http.Response, request *http.Request, channel model.ModelChannel, context aiLogContext, onFailure func()) bool {
	official := service.IsMiniMaxChannel(channel) && service.IsMiniMaxTTSModelName(context.Model)
	tokenDanceModel := strings.ToLower(strings.TrimSpace(context.Model))
	tokenDance := service.IsTokenDanceChannel(channel) &&
		strings.HasPrefix(tokenDanceModel, "minimax-") &&
		service.IsMiniMaxTTSModelName(strings.TrimPrefix(tokenDanceModel, "minimax-")) &&
		strings.HasSuffix(strings.ToLower(request.URL.Path), "/minimax/v1/t2a_v2")
	if context.Endpoint != "/audio/speech" || !official && !tokenDance {
		return false
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		if onFailure != nil {
			onFailure()
		}
		saveAIProxyLog(context, response.StatusCode, "", err.Error())
		Fail(w, "MiniMax TTS 响应读取失败")
		return true
	}
	var result struct {
		Data struct {
			Audio string `json:"audio"`
		} `json:"data"`
		ExtraInfo struct {
			AudioFormat string `json:"audio_format"`
		} `json:"extra_info"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		if onFailure != nil {
			onFailure()
		}
		saveAIProxyLog(context, response.StatusCode, string(payload), err.Error())
		Fail(w, "MiniMax TTS 响应格式错误")
		return true
	}
	if result.BaseResp.StatusCode != 0 || strings.TrimSpace(result.Data.Audio) == "" {
		if onFailure != nil {
			onFailure()
		}
		message := strings.TrimSpace(result.BaseResp.StatusMsg)
		if message == "" {
			message = "MiniMax TTS 未返回音频数据"
		}
		saveAIProxyLog(context, response.StatusCode, string(payload), message)
		Fail(w, message)
		return true
	}
	audio, err := hex.DecodeString(strings.TrimSpace(result.Data.Audio))
	if err != nil {
		if onFailure != nil {
			onFailure()
		}
		saveAIProxyLog(context, response.StatusCode, "[invalid hex audio]", err.Error())
		Fail(w, "MiniMax TTS 音频解码失败")
		return true
	}
	mimeType := map[string]string{
		"mp3":  "audio/mpeg",
		"wav":  "audio/wav",
		"flac": "audio/flac",
	}[strings.ToLower(result.ExtraInfo.AudioFormat)]
	if mimeType == "" {
		mimeType = http.DetectContentType(audio)
	}
	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(audio)
	saveAIProxyLog(context, response.StatusCode, "[binary audio]", "")
	return true
}
