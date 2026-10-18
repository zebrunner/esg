package monitoring

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/service"
)

var recorderClient = &http.Client{
	Timeout:       3 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
var proxySlots = make(chan struct{}, 8)

func TaskToken(workspace, esgID string) string {
	data, _ := json.Marshal([]string{"v1", workspace, esgID})
	mac := hmac.New(sha256.New, []byte(config.Conf.MonitoringIntegrationToken))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func RegisterRoutes(group *gin.RouterGroup, admin bool) {
	group.GET("/stats/:session", summaryHandler(false, admin))
	group.GET("/tasks/:task/stats", summaryHandler(true, admin))
}

func summaryHandler(generic, admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		select {
		case proxySlots <- struct{}{}:
			defer func() { <-proxySlots }()
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "monitoring_busy"})
			return
		}
		id, folder := c.Param("session"), "test-sessions"
		if generic {
			id, folder = c.Param("task"), "launches"
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_esg_id"})
			return
		}
		entity, err := mapper.Find(id)
		if err != nil && !errors.Is(err, redis.Nil) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "task_cache_unavailable"})
			return
		}
		workspace, _, _ := c.Request.BasicAuth()
		if admin {
			workspace = c.Query("workspace")
			if entity != nil {
				workspace = entity.Workspace
			}
		} else if entity != nil && entity.Workspace != workspace {
			c.JSON(http.StatusNotFound, gin.H{"error": "task_not_found"})
			return
		}
		if workspace == "" || len(workspace) > 1024 || strings.ContainsRune(workspace, 0) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_required"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		if entity != nil {
			platform := ""
			if entity.Capabilities != nil {
				platform = strings.ToLower(entity.Capabilities.PlatformName.ToPrimitive())
			}
			if generic != (platform == "generic") || platform == "windows" || platform == "cypress" {
				c.JSON(http.StatusNotFound, gin.H{"error": "unsupported_task_type"})
				return
			}
			id = entity.RouterUUID
			if entity.Status == mapper.Queued {
				c.JSON(http.StatusConflict, gin.H{"error": "task_queued"})
				return
			}
			if entity.Status == mapper.Active {
				if !config.Conf.MonitoringEnabled {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "monitoring_disabled"})
					return
				}
				target, stopped, err := recorderURL(ctx, entity)
				if err != nil {
					c.JSON(http.StatusBadGateway, gin.H{"error": "recorder_address_unavailable"})
					return
				}
				if !stopped {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
					if err != nil {
						c.JSON(http.StatusBadGateway, gin.H{"error": "recorder_address_invalid"})
						return
					}
					req.Header.Set("Authorization", "Bearer "+TaskToken(workspace, id))
					response, err := recorderClient.Do(req)
					if err == nil {
						defer response.Body.Close()
						body, readErr := io.ReadAll(io.LimitReader(response.Body, 8*1024+1))
						var identity struct {
							Version int    `json:"version"`
							EsgID   string `json:"esgId"`
							TaskARN string `json:"taskArn"`
						}
						if response.StatusCode != http.StatusOK || readErr != nil || len(body) > 8*1024 || json.Unmarshal(body, &identity) != nil || identity.Version != 1 || identity.EsgID != id || entity.TaskId == "" || !strings.HasSuffix(identity.TaskARN, "/"+entity.TaskId) {
							c.JSON(http.StatusBadGateway, gin.H{"error": "recorder_summary_unavailable"})
							return
						}
						c.Data(http.StatusOK, "application/json", body)
						return
					}
					if !taskStopped(ctx, entity.TaskId) {
						c.JSON(http.StatusBadGateway, gin.H{"error": "recorder_unavailable"})
						return
					}
				}
			}
		}
		key := strings.Join([]string{workspace, "artifacts", folder, id, "stats.json"}, "/")
		location, err := service.GeneratePreSignedURL(ctx, key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "monitoring_summary_unavailable"})
			return
		}
		c.Redirect(http.StatusFound, location)
	}
}
