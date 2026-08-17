package service

import (
	"context"
	"strings"

	"github.com/tigerowo/infinite-canvas/config"
	"github.com/tigerowo/infinite-canvas/model"
)

type userContextKey struct{}
type requestOriginContextKey struct{}

func WithUser(ctx context.Context, user model.AuthUser) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

func UserFromContext(ctx context.Context) (model.AuthUser, bool) {
	user, ok := ctx.Value(userContextKey{}).(model.AuthUser)
	return user, ok
}

// WithRequestOrigin 保存发起请求的站点根地址，供异步任务生成浏览器可访问的完整 URL。
func WithRequestOrigin(ctx context.Context, origin string) context.Context {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		return ctx
	}
	return context.WithValue(ctx, requestOriginContextKey{}, origin)
}

func requestOriginFromContext(ctx context.Context) string {
	origin, _ := ctx.Value(requestOriginContextKey{}).(string)
	return strings.TrimRight(strings.TrimSpace(origin), "/")
}

func absoluteAppURL(ctx context.Context, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.Cfg.PublicBaseURL), "/")
	if baseURL == "" {
		baseURL = requestOriginFromContext(ctx)
	}
	if baseURL == "" {
		return value
	}
	return baseURL + "/" + strings.TrimLeft(value, "/")
}
