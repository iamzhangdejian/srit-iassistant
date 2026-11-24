package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/licenser"
	"github.com/Tencent/WeKnora/internal/logger"
)

// License 是授权闸门中间件。
//
// 它必须挂在 Auth 之后（拿得到用户身份）但要在业务路由之前，原因：
//  1. 无授权时也要能登录，否则用户看不到授权页，形成死锁；
//  2. 授权是「机器级」约束，与「哪个用户」无关，所以放在认证之后
//     只是为了复用同一条链路，不依赖用户身份。
//
// 用 403 而不是 401：前端拦截器把 401 当作「刷新令牌失败 → 跳登录页」，
// 如果这里返回 401，用户会陷入「登录页 ↔ 聊天页」的循环，永远看不到
// 授权页。403 不会被拦截器接管，前端可以自己决定跳转到 /license。
func License(service *licenser.Service) gin.HandlerFunc {
	if service == nil || !service.Enabled() {
		// 未启用授权时完全不注入中间件，连一次函数调用都不多花。
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		decision := service.Gate(path)
		if decision.Allowed {
			c.Next()
			return
		}

		logger.Warnf(c.Request.Context(),
			"[license] blocked %s %s: code=%s state=%s",
			c.Request.Method, path, decision.Code, service.Status().State)

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"error": gin.H{
				// code 让前端区分「没授权（跳授权页）」与
				// 「功能未开通（就地提示）」，别把两者混成一条文案。
				"code":    decision.Code,
				"message": decision.Message,
			},
		})
	}
}