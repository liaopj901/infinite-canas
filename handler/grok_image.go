package handler

import (
	"math"
	"strconv"
	"strings"
)

// Grok 图片接口只接受固定比例；这里把旧配置和自定义尺寸收敛到最接近的合法值。
func normalizeGrokImageAspect(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(strings.ToLower(value)), " ", "")
	switch value {
	case "square", "square_hd":
		return "1:1"
	case "landscape":
		return "16:9"
	case "portrait":
		return "9:16"
	}

	width, height, ok := parseGrokImageAspect(value)
	if !ok {
		return "1:1"
	}

	ratio := float64(width) / float64(height)
	bestName := "1:1"
	bestDiff := math.MaxFloat64
	for _, option := range []struct {
		name  string
		ratio float64
	}{
		{"1:1", 1},
		{"3:2", 3.0 / 2.0},
		{"2:3", 2.0 / 3.0},
		{"16:9", 16.0 / 9.0},
		{"9:16", 9.0 / 16.0},
	} {
		if diff := math.Abs(ratio - option.ratio); diff < bestDiff {
			bestName = option.name
			bestDiff = diff
		}
	}
	return bestName
}

func isAPIMartGrokImageAspectModel(modelName string) bool {
	switch normalizeAPIMartModelName(modelName) {
	case "grok-imagine-1-5-apimart", "grok-imagine-1-5-ext":
		return true
	default:
		return false
	}
}

func parseGrokImageAspect(value string) (int, int, bool) {
	var parts []string
	switch {
	case strings.Contains(value, "x"):
		parts = strings.Split(value, "x")
	case strings.Contains(value, "*"):
		parts = strings.Split(value, "*")
	case strings.Contains(value, ":"):
		parts = strings.Split(value, ":")
	default:
		return 0, 0, false
	}
	if len(parts) != 2 {
		return 0, 0, false
	}

	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}
