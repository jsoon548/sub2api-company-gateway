package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestGovernanceRoutesReplacePhysicalUserDeletion(t *testing.T) {
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUserRole), service.RoleSuperAdmin)
		c.Set("auth_method", "jwt")
		c.Next()
	})
	RegisterAdminRoutes(v1, &handler.Handlers{Admin: &handler.AdminHandlers{}}, auth, nil)

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, required := range []string{
		"GET /api/v1/admin/governance/super-admin/seat",
		"POST /api/v1/admin/governance/super-admin/transfer",
		"POST /api/v1/admin/users/:id/deactivate",
		"POST /api/v1/admin/users/:id/reactivate",
		"GET /api/v1/admin/audit/interactions",
		"POST /api/v1/admin/audit/interactions/:id/disclose",
		"GET /api/v1/admin/gateway-usage",
		"GET /api/v1/admin/gateway-usage/summary",
		"GET /api/v1/admin/gateway-usage/:gateway_request_id",
	} {
		if !routes[required] {
			t.Errorf("missing governance route %s", required)
		}
	}
	if routes["DELETE /api/v1/admin/users/:id"] {
		t.Fatal("physical user deletion route must not be registered")
	}
}
