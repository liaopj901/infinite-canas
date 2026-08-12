package handler

import (
	"strings"
	"testing"
)

func TestPrepareDirectAIRequestKIEReferences(t *testing.T) {
	markers := map[string]string{
		"image1": "https://direct-reference.invalid/run/image/0",
		"image2": "https://direct-reference.invalid/run/image/1",
		"video":  "https://direct-reference.invalid/run/video/2",
		"audio":  "https://direct-reference.invalid/run/audio/3",
	}
	plan, err := prepareDirectAIRequest(directAIRequestInput{
		Channel:  directAIChannelInput{Protocol: "kie", BaseURL: "https://api.kie.ai"},
		Model:    "bytedance/seedance-2",
		Endpoint: "/videos",
		Body: map[string]any{
			"prompt":            "test",
			"input_reference[]": []any{markers["image1"], markers["image2"]},
			"video_reference[]": []any{markers["video"]},
			"audio_reference[]": []any{markers["audio"]},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Provider != "kie" || !strings.HasSuffix(plan.URL, "/v1/jobs/createTask") {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	input := testDirectRecord(t, testDirectRecord(t, plan.Body)["input"])
	testDirectStrings(t, input["reference_image_urls"], markers["image1"], markers["image2"])
	testDirectStrings(t, input["reference_video_urls"], markers["video"])
	testDirectStrings(t, input["reference_audio_urls"], markers["audio"])
	for _, kind := range []string{"image", "video", "audio"} {
		if _, ok := plan.Uploads[kind]; !ok {
			t.Fatalf("missing %s upload plan", kind)
		}
	}
}

func TestPrepareDirectAIRequestAPIMartImageReferences(t *testing.T) {
	markers := []any{
		"https://direct-reference.invalid/run/image/0",
		"https://direct-reference.invalid/run/image/1",
	}
	plan, err := prepareDirectAIRequest(directAIRequestInput{
		Channel:  directAIChannelInput{Protocol: "openai", BaseURL: "https://api.apimart.ai"},
		Model:    "gpt-image-2-apimart",
		Endpoint: "/images/edits",
		Body: map[string]any{
			"prompt": "test",
			"image":  markers,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Provider != "apimart" || !strings.HasSuffix(plan.URL, "/v1/images/generations") {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	payload := testDirectRecord(t, plan.Body)
	testDirectStrings(t, payload["image_urls"], markers[0].(string), markers[1].(string))
	if _, ok := plan.Uploads["image"]; !ok {
		t.Fatal("missing image upload plan")
	}
	if _, ok := plan.Uploads["video"]; ok {
		t.Fatal("unexpected video upload plan")
	}
}

func TestPrepareDirectAIRequestRejectsMediaData(t *testing.T) {
	_, err := prepareDirectAIRequest(directAIRequestInput{
		Channel:  directAIChannelInput{Protocol: "kie", BaseURL: "https://api.kie.ai"},
		Model:    "bytedance/seedance-2",
		Endpoint: "/videos",
		Body:     map[string]any{"image": "data:image/png;base64,AAAA"},
	})
	if err == nil || !strings.Contains(err.Error(), "参考文件不能传给参数转译接口") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testDirectRecord(t *testing.T, value any) map[string]any {
	t.Helper()
	record, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %#v", value)
	}
	return record
}

func testDirectStrings(t *testing.T, value any, expected ...string) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected array, got %#v", value)
	}
	if len(items) != len(expected) {
		t.Fatalf("expected %d items, got %#v", len(expected), items)
	}
	for index, item := range items {
		if item != expected[index] {
			t.Fatalf("unexpected item %d: %#v", index, item)
		}
	}
}
