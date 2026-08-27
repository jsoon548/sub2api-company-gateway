package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminOnly 管理员权限中间件
// 必须在JWTAuth中间件之后使用
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetUserRoleFromContext(c)
		if !ok {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}

		// 检查是否为管理员
		if !(service.Authorizer{Role: role}).Has(service.CapabilityAdminAccess) {
			AbortWithError(c, 403, "FORBIDDEN", "Admin access required")
			return
		}

		c.Next()
	}
}

// RequireCapability centralizes role-to-capability authorization.
func RequireCapability(capability service.Capability, namedSessionOnly bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetUserRoleFromContext(c)
		if !ok {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}
		if !(service.Authorizer{Role: role}).Has(capability) {
			AbortWithError(c, 403, "FORBIDDEN", "Insufficient capability")
			return
		}
		if namedSessionOnly && c.GetString("auth_method") != "jwt" {
			AbortWithError(c, 403, "NAMED_SESSION_REQUIRED", "Named administrator session required")
			return
		}
		c.Next()
	}
}
