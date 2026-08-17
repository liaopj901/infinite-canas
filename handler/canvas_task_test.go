package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanvasImageTaskResponseBodyContainsOnlyURLs(t *testing.T) {
	body := canvasImageTaskResponseBody([]string{
		"/api/v1/generated-images/local-id/content",
		"https://cdn.example.com/image.png",
	})
	if strings.Contains(body, "base64") || strings.Contains(body, "data:image") {
		t.Fatalf("response body contains image bytes: %s", body)
	}
	var payload struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(payload.Data) != 2 || payload.Data[0].URL != "/api/v1/generated-images/local-id/content" || payload.Data[1].URL != "https://cdn.example.com/image.png" {
		t.Fatalf("unexpected response body: %s", body)
	}
}
