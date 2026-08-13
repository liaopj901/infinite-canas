package handler

import (
	"net/http"
	"testing"
)

func TestShouldRetryImageUpstream(t *testing.T) {
	tests := []struct {
		name       string
		retryImage bool
		status     int
		body       string
		want       bool
	}{
		{name: "empty 502", retryImage: true, status: http.StatusBadGateway, want: true},
		{name: "html 502", retryImage: true, status: http.StatusBadGateway, body: "<html>bad gateway</html>", want: true},
		{name: "known nested error", retryImage: true, status: http.StatusBadGateway, body: `{"error":{"message":"余额不足"}}`, want: false},
		{name: "known message", retryImage: true, status: http.StatusInternalServerError, body: `{"msg":"参数错误"}`, want: false},
		{name: "client error", retryImage: true, status: http.StatusBadRequest, want: false},
		{name: "non image request", retryImage: false, status: http.StatusBadGateway, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldRetryImageUpstream(test.retryImage, test.status, []byte(test.body)); got != test.want {
				t.Fatalf("shouldRetryImageUpstream() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIsImageAIRequest(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		body     string
		want     bool
	}{
		{name: "image generation", endpoint: "/images/generations", want: true},
		{name: "image edit", endpoint: "/images/edits", want: true},
		{name: "image responses", endpoint: "/responses", body: `{"tools":[{"type":"image_generation"}]}`, want: true},
		{name: "text responses", endpoint: "/responses", body: `{"tools":[{"type":"web_search"}]}`, want: false},
		{name: "invalid responses body", endpoint: "/responses", body: `{`, want: false},
		{name: "chat completion", endpoint: "/chat/completions", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isImageAIRequest(test.endpoint, []byte(test.body)); got != test.want {
				t.Fatalf("isImageAIRequest() = %v, want %v", got, test.want)
			}
		})
	}
}
