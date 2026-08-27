package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestSharedAdminAPIKeyExactReadOnlyAllowlist(t *testing.T) {
	allowed := map[string]bool{
		"/api/v1/admin/dashboard/stats":                true,
		"/api/v1/admin/ops/account-availability":       true,
		"/api/v1/admin/ops/system-logs/health":         true,
		"/api/v1/admin/governance/super-admin/seat":    false,
		"/api/v1/admin/audit/interactions":             false,
		"/api/v1/admin/audit/interactions/id/disclose": false,
		"/api/v1/admin/gateway-usage":                  false,
		"/api/v1/admin/gateway-usage/summary":          false,
		"/api/v1/admin/gateway-usage/id":               false,
		"/api/v1/admin/users":                          false,
		"/api/v1/admin/settings":                       false,
		"/api/v1/admin/accounts":                       false,
		"/api/v1/admin/ops/system-logs":                false,
		"/api/v1/admin/dashboard/stats/":               false,
		"/api/v1/admin/dashboard/stats?synthetic=true": false,
	}
	for path, want := range allowed {
		if got := sharedAdminAPIKeyRouteAllowed(http.MethodGet, path); got != want {
			t.Errorf("GET %s got=%v want=%v", path, got, want)
		}
	}
	for _, path := range []string{
		"/api/v1/admin/dashboard/stats",
		"/api/v1/admin/ops/account-availability",
		"/api/v1/admin/ops/system-logs/health",
	} {
		if sharedAdminAPIKeyRouteAllowed(http.MethodPost, path) || sharedAdminAPIKeyRouteAllowed(http.MethodPut, path) || sharedAdminAPIKeyRouteAllowed(http.MethodDelete, path) {
			t.Errorf("write unexpectedly allowed for %s", path)
		}
	}
}

func TestAuditManagementRawContentCapabilityMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, role, method string
		want               int
	}{
		{name: "named_super_admin", role: service.RoleSuperAdmin, method: "jwt", want: http.StatusNoContent},
		{name: "ordinary_admin", role: service.RoleAdmin, method: "jwt", want: http.StatusForbidden},
		{name: "user", role: service.RoleUser, method: "jwt", want: http.StatusForbidden},
		{name: "shared_admin_key", role: service.RoleSuperAdmin, method: "admin_api_key", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUserRole), tc.role)
				c.Set("auth_method", tc.method)
			})
			router.Use(RequireCapability(service.CapabilityRawContentDisclosure, true))
			router.POST("/disclose", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/disclose", nil))
			if recorder.Code != tc.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSuperAdminPassesAdminOnly(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUserRole), service.RoleSuperAdmin)
	})
	router.Use(AdminOnly())
	router.GET("/admin", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequireCapabilityRequiresNamedSuperAdminSession(t *testing.T) {
	for _, tc := range []struct {
		name, role, method string
		want               int
	}{
		{name: "named_super_admin", role: service.RoleSuperAdmin, method: "jwt", want: http.StatusNoContent},
		{name: "ordinary_admin", role: service.RoleAdmin, method: "jwt", want: http.StatusForbidden},
		{name: "shared_key_cannot_impersonate", role: service.RoleSuperAdmin, method: "admin_api_key", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUserRole), tc.role)
				c.Set("auth_method", tc.method)
			})
			router.Use(RequireCapability(service.CapabilityTransferSuperAdmin, true))
			router.GET("/seat", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/seat", nil))
			if recorder.Code != tc.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
