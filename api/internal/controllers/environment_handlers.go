package controllers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// DeleteEnvironment removes only the observation journal, never workout videos or sensors.
// The exact profile prefix is authorized even if video analysis never created a session row.
func (ctl *Controller) DeleteEnvironment(c *gin.Context) {
	if UserIDFromContext(c) == 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	sessionID := c.Param("session_id")
	profile, err := strconv.ParseUint(c.Query("profile_id"), 10, 32)
	if err != nil || profile == 0 || !isValidSessionID(sessionID) || strings.ContainsAny(sessionID, "/\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session or profile"})
		return
	}
	if !ctl.assertOwnsProfile(c, uint(profile)) {
		return
	}
	prefix := fmt.Sprintf("videos/%d/%s/environment_", profile, sessionID)
	objects, err := ctl.storageClient.ListObjects(c.Request.Context(), prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list observation files"})
		return
	}
	for _, object := range objects {
		if !strings.HasPrefix(object, prefix) || strings.Contains(strings.TrimPrefix(object, prefix), "/") {
			continue
		}
		if err := ctl.storageClient.DeleteObject(c.Request.Context(), object); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete observation files; retry safely"})
			return
		}
	}
	c.Status(http.StatusNoContent)
}
