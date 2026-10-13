package admin

import (
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func miMoLoginOwner(c *gin.Context) int64 {
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		return subject.UserID
	}
	return 0
}
func (h *AccountHandler) StartMiMoLogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := h.mimoLogin.Start(c.Request.Context(), miMoLoginOwner(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) MiMoLoginStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := h.mimoLogin.Status(miMoLoginOwner(c), c.Param("session"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) CancelMiMoLogin(c *gin.Context) {
	h.mimoLogin.Cancel(miMoLoginOwner(c), c.Param("session"))
	response.Success(c, gin.H{"success": true})
}

// Keep business cookies on the server. The browser submits only a short-lived,
// administrator-bound handle; the handle is never persisted as a credential.
func (h *AccountHandler) resolveMiMoLogin(c *gin.Context, platform string, credentials map[string]any) (map[string]any, error) {
	raw, exists := credentials[service.MiMoLoginSessionCredential]
	if !exists {
		return credentials, nil
	}
	id, ok := raw.(string)
	if !ok || id == "" || platform != service.PlatformMiMo || credentials["account_mode"] != service.MiMoModeDesktop {
		return nil, apperrors.BadRequest("INVALID_MIMO_LOGIN", "Xiaomi login can only authorize MiMo Desktop accounts")
	}
	cookie, err := h.mimoLogin.Cookie(miMoLoginOwner(c), id)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(credentials))
	for k, v := range credentials {
		if k != service.MiMoLoginSessionCredential {
			result[k] = v
		}
	}
	result["mimo_session_cookie"] = cookie
	return result, nil
}
func (h *AccountHandler) finishMiMoLogin(c *gin.Context, credentials map[string]any) {
	if id, ok := credentials[service.MiMoLoginSessionCredential].(string); ok {
		h.mimoLogin.Cancel(miMoLoginOwner(c), id)
	}
}
