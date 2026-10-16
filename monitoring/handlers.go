package monitoring

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/service"
)

var recorderClient = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func RegisterRoutes(group *gin.RouterGroup, admin bool) {
	group.GET("/stats/:session", artifactHandler("stats.json", "recorderStats", false, admin))
	group.GET("/containers-log/:session", artifactHandler("containers.log", "recorderLogs", false, admin))
	group.GET("/tasks/:task/stats", artifactHandler("stats.json", "recorderStats", true, admin))
	group.GET("/tasks/:task/containers-log", artifactHandler("containers.log", "recorderLogs", true, admin))
}

func artifactHandler(file, endpoint string, generic, admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("session")
		folder := "test-sessions"
		if generic {
			id, folder = c.Param("task"), "launches"
		}
		if _, err := uuid.Parse(id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ESG ID"})
			return
		}

		entity, err := mapper.Find(id)
		if err != nil && !errors.Is(err, redis.Nil) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "task cache unavailable"})
			return
		}
		workspace, _, _ := c.Request.BasicAuth()
		if admin {
			workspace = c.Query("workspace")
			if entity != nil {
				workspace = entity.Workspace
			}
			if workspace == "" || strings.ContainsAny(workspace, "/\\") || workspace == "." || workspace == ".." {
				c.JSON(http.StatusBadRequest, gin.H{"error": "workspace is required"})
				return
			}
		} else if entity != nil && entity.Workspace != workspace {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}

		c.Header("Cache-Control", "no-store")
		if entity != nil {
			platform := ""
			if entity.Capabilities != nil {
				platform = strings.ToLower(entity.Capabilities.PlatformName.ToPrimitive())
			}
			if generic != (platform == "generic") || platform == "windows" || platform == "cypress" {
				c.JSON(http.StatusNotFound, gin.H{"error": "task monitoring is not available for this task type"})
				return
			}
			id = entity.RouterUUID
			if entity.Status == mapper.Queued {
				c.JSON(http.StatusConflict, gin.H{"error": "task is queued"})
				return
			}
			if entity.Status == mapper.Active {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
				target, stopped, err := recorderURL(ctx, entity, endpoint)
				cancel()
				if err != nil {
					c.JSON(http.StatusBadGateway, gin.H{"error": "recorder address unavailable"})
					return
				}
				if !stopped {
					req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target.String(), nil)
					if err != nil {
						c.JSON(http.StatusBadGateway, gin.H{"error": "invalid recorder address"})
						return
					}
					req.Host = "localhost"
					response, err := recorderClient.Do(req)
					if err != nil {
						ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
						stopped = taskStopped(ctx, entity.TaskId)
						cancel()
						if !stopped {
							c.JSON(http.StatusBadGateway, gin.H{"error": "recorder unavailable"})
							return
						}
					} else {
						defer response.Body.Close()
						c.DataFromReader(response.StatusCode, response.ContentLength, response.Header.Get("Content-Type"), response.Body, nil)
						return
					}
				}
			}
		}
		key := strings.Join([]string{workspace, "artifacts", folder, id, file}, "/")
		location, err := service.GeneratePreSignedURL(c.Request.Context(), key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "monitoring artifact not found"})
			return
		}
		c.Redirect(http.StatusFound, location)
	}
}
