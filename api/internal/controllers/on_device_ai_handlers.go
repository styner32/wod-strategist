package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
)

var onDeviceAssetName = regexp.MustCompile(`^(apple_ai_|environment_)[A-Za-z0-9_-]+\.(json|jpg|m4a|ndjson)$`)

// Authorize the exact profile prefix, including sessions without a cloud analysis row.
func (ctl *Controller) onDevicePrefix(c *gin.Context) (string, bool) {
	c.Header("Cache-Control", "private, no-store")
	if UserIDFromContext(c) == 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return "", false
	}
	sid := c.Param("session_id")
	pid, err := strconv.ParseUint(c.Query("profile_id"), 10, 32)
	if err != nil || pid == 0 || !isValidSessionID(sid) || strings.ContainsAny(sid, "/\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session or profile"})
		return "", false
	}
	if !ctl.assertOwnsProfile(c, uint(pid)) {
		return "", false
	}
	return fmt.Sprintf("videos/%d/%s/", pid, sid), true
}

// ListOnDeviceAI returns only journal manifests/telemetry, never video or sensor assets.
// Bodies and media are fetched on demand, not for every list item.
func (ctl *Controller) ListOnDeviceAI(c *gin.Context) {
	prefix, ok := ctl.onDevicePrefix(c)
	if !ok {
		return
	}
	after := c.Query("after")
	if after != "" && !onDeviceAssetName.MatchString(after) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
		return
	}
	files := make([]string, 0)
	for _, family := range []string{"apple_ai_", "environment_"} {
		objects, err := ctl.storageClient.ListObjects(c.Request.Context(), prefix+family)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "could not list on-device records"})
			return
		}
		for _, object := range objects {
			if !strings.HasPrefix(object, prefix) {
				continue
			}
			name := strings.TrimPrefix(object, prefix)
			if onDeviceAssetName.MatchString(name) && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".ndjson")) && (after == "" || name > after) {
				files = append(files, name)
			}
		}
	}
	sort.Strings(files)
	next := ""
	if len(files) > 50 {
		files = files[:50]
		next = files[49]
	}
	c.JSON(http.StatusOK, gin.H{"files": files, "next_cursor": next})
}

// GetOnDeviceAIAsset proxies small JSON documents to avoid bucket CORS requirements.
// Media and telemetry use short-lived signed URLs, avoiding API-side media buffering.
func (ctl *Controller) GetOnDeviceAIAsset(c *gin.Context) {
	prefix, ok := ctl.onDevicePrefix(c)
	if !ok {
		return
	}
	name := c.Query("filename")
	if !onDeviceAssetName.MatchString(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid observation filename"})
		return
	}
	if !strings.HasSuffix(name, ".json") {
		url, err := ctl.storageClient.GenerateSignedURL(prefix+name, http.MethodGet, 15*time.Minute)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "could not open evidence"})
			return
		}
		c.Redirect(http.StatusTemporaryRedirect, url)
		return
	}
	// -1 selects the current generation; these archived JSON files are immutable.
	reader, err := ctl.storageClient.NewReaderWithGeneration(c.Request.Context(), prefix+name, -1)
	if errors.Is(err, gcs.ErrObjectNotExist) {
		c.JSON(http.StatusNotFound, gin.H{"error": "record not uploaded or no longer available"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not read observation"})
		return
	}
	defer reader.Close()
	const maxJSON = 2 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(reader, maxJSON+1))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not read observation"})
		return
	}
	if len(body) > maxJSON {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "observation exceeds 2 MB limit"})
		return
	}
	var record map[string]json.RawMessage
	if json.Unmarshal(body, &record) != nil || record == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid observation JSON"})
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}
