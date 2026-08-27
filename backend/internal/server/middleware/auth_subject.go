package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
)

// AuthSubject is the minimal authenticated identity stored in gin context.
// Named-session governance also carries the authoritative session epoch and
// token expiry so sensitive actions can revalidate them before returning data.
type AuthSubject struct {
	UserID           int64
	Concurrency      int
	SessionVersion   int64
	SessionExpiresAt time.Time
}

func GetAuthSubjectFromContext(c *gin.Context) (AuthSubject, bool) {
	value, exists := c.Get(string(ContextKeyUser))
	if !exists {
		return AuthSubject{}, false
	}
	subject, ok := value.(AuthSubject)
	return subject, ok
}

func GetUserRoleFromContext(c *gin.Context) (string, bool) {
	value, exists := c.Get(string(ContextKeyUserRole))
	if !exists {
		return "", false
	}
	role, ok := value.(string)
	return role, ok
}
