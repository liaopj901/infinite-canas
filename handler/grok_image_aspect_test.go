package handler

import (
	"testing"

	"github.com/tigerowo/infinite-canvas/model"
)

func TestNormalizeGrokImageAspect(t *testing.T) {
	tests := map[string]string{
		"1536x1024": "3:2",
		"1024x1536": "2:3",
		"2048x1152": "16:9",
		"1152x2048": "9:16",
		"2048x2048": "1:1",
		"4:3":       "3:2",
		"3:4":       "2:3",
		"21:9":      "16:9",
		"auto":      "1:1",
	}
	for input, expected := range tests {
		if actual := normalizeGrokImageAspect(input); actual != expected {
			t.Fatalf("normalizeGrokImageAspect(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestAPIMartGrokImageAspectDoesNotAffectGPT(t *testing.T) {
	grokPayload := map[string]any{"size": "1568x672"}
	normalizeAPIMartImageParams(grokPayload, "grok-imagine-1.5-apimart", model.ModelChannel{})
	if grokPayload["size"] != "16:9" {
		t.Fatalf("unexpected Grok size: %#v", grokPayload["size"])
	}

	emptyGrokPayload := map[string]any{}
	normalizeAPIMartImageParams(emptyGrokPayload, "grok-imagine-1.5-apimart", model.ModelChannel{})
	if _, exists := emptyGrokPayload["size"]; exists {
		t.Fatalf("Grok size must remain omitted: %#v", emptyGrokPayload["size"])
	}

	gptPayload := map[string]any{"size": "1024x768"}
	normalizeAPIMartImageParams(gptPayload, "gpt-image-2-apimart", model.ModelChannel{})
	if gptPayload["size"] != "4:3" {
		t.Fatalf("GPT size changed: %#v", gptPayload["size"])
	}
}

func TestKIEGrokImageAspectDoesNotAffectGPT(t *testing.T) {
	grokInput := map[string]any{"size": "1536x1024"}
	setKIEAspectInput(grokInput, "grok-imagine/text-to-image", grokInput["size"])
	if grokInput["aspect_ratio"] != "3:2" {
		t.Fatalf("unexpected Grok aspect ratio: %#v", grokInput["aspect_ratio"])
	}

	grokEditInput := map[string]any{"size": "1536x1024"}
	setKIEAspectInput(grokEditInput, "grok-imagine/image-to-image", grokEditInput["size"])
	if _, exists := grokEditInput["aspect_ratio"]; exists {
		t.Fatalf("Grok image-to-image must not send aspect_ratio: %#v", grokEditInput)
	}
	if _, exists := grokEditInput["size"]; exists {
		t.Fatalf("Grok image-to-image must not send size: %#v", grokEditInput)
	}

	gptInput := map[string]any{"size": "1024x768"}
	setKIEAspectInput(gptInput, "gpt-image/1.5-text-to-image", gptInput["size"])
	if gptInput["aspect_ratio"] != "4:3" {
		t.Fatalf("GPT aspect ratio changed: %#v", gptInput["aspect_ratio"])
	}
}
