package service

import (
	"strings"

	"github.com/tigerowo/infinite-canvas/model"
)

const ModelChannelProtocolMiniMax = "minimax"

var miniMaxModels = [...]string{
	"MiniMax-H3",
	"speech-2.8-hd", "speech-2.8-turbo",
	"speech-2.6-hd", "speech-2.6-turbo",
	"speech-02-hd", "speech-02-turbo",
	"speech-01-hd", "speech-01-turbo",
}

func MiniMaxModels() []string {
	return append([]string(nil), miniMaxModels[:]...)
}

func IsMiniMaxChannel(channel model.ModelChannel) bool {
	return strings.EqualFold(strings.TrimSpace(channel.Protocol), ModelChannelProtocolMiniMax)
}

func IsMiniMaxH3ModelName(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), miniMaxModels[0])
}

func IsMiniMaxTTSModelName(modelName string) bool {
	value := strings.ToLower(strings.TrimSpace(modelName))
	for _, item := range miniMaxModels[1:] {
		if value == item {
			return true
		}
	}
	return false
}
