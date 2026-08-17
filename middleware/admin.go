package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tigerowo/infinite-canvas/handler"
	"github.com/tigerowo/infinite-canvas/model"
	"github.com/tigerowo/infinite-canvas/service"
)

func AdminAuth(c *gin.Context) {
	user, ok := authUser(c)
	if !ok || user.Role != model.UserRoleAdmin {
		handler.FailWithStatus(c.Writer, http.StatusUnauthorized, "未登录或权限不足")
		c.Abort()
		return
	}
	setRequestContext(c, &user)
	c.Next()
}

func UserAuth(c *gin.Context) {
	user, ok := authUser(c)
	if !ok || user.Role == model.UserRoleGuest {
		handler.FailWithStatus(c.Writer, http.StatusUnauthorized, "未登录或权限不足")
		c.Abort()
		return
	}
	setRequestContext(c, &user)
	c.Next()
}

func OptionalAuth(c *gin.Context) {
	if user, ok := authUser(c); ok {
		setRequestContext(c, &user)
	} else {
		setRequestContext(c, nil)
	}
	c.Next()
}

func setRequestContext(c *gin.Context, user *model.AuthUser) {
	ctx := service.WithRequestOrigin(c.Request.Context(), service.RequestOrigin(c.Request))
	if user != nil {
		ctx = service.WithUser(ctx, *user)
	}
	c.Request = c.Request.WithContext(ctx)
}

func NotFoundJSON(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"code": 1, "data": nil, "msg": "接口不存在"})
}

func authUser(c *gin.Context) (model.AuthUser, bool) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if strings.TrimSpace(token) == "" {
		return model.AuthUser{}, false
	}
	return service.CurrentAuthUser(token)
}
