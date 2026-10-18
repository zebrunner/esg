package monitoring

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment/network"
)

var taskPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type cachedTask struct {
	RouterUUID        string
	TaskId            string
	Workspace         string
	MonitoringSession string
	MonitoringEnvType string
	MonitoringTaskARN string
	Status            int
	StopReason        string
	Network           network.NetworkConfiguration
	HealthAt          *time.Time
}

func Register(r gin.IRouter) {
	group := r.Group("/internal/monitoring/v1", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if !config.Conf.MonitoringCompatibilityEnabled {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "monitoring_disabled", "version": 1})
			return
		}
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || !hmac.Equal([]byte(token), []byte(config.Conf.MonitoringIntegrationToken)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication_required"})
			return
		}
		if redisClient() == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "storage_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		c.Next()
	})
	group.GET("/session", sessionStatus)
	group.POST("/session", session)
	group.DELETE("/session", session)
	group.GET("/tasks", tasks)
	group.POST("/ack", acknowledge)
}

func sessionStatus(c *gin.Context) {
	ctx := c.Request.Context()
	client := redisClient()
	pipe := client.Pipeline()
	active := pipe.Get(ctx, leaseKey)
	ttl := pipe.TTL(ctx, leaseKey)
	expired := pipe.Get(ctx, "{esg-monitoring}:expired")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		storageError(c)
		return
	}
	count, _ := expired.Int64()
	c.JSON(http.StatusOK, gin.H{"version": 1, "sessionId": active.Val(), "leaseSeconds": max(0, int(ttl.Val().Seconds())), "expiredDescriptors": count})
}

func session(c *gin.Context) {
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if c.ShouldBindJSON(&body) != nil || !validUUID(body.SessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_session"})
		return
	}
	script := `local owner=redis.call('GET',KEYS[1]); if owner and owner~=ARGV[1] then return 0 end; redis.call('SET',KEYS[1],ARGV[1],'EX',90); return 1`
	if c.Request.Method == http.MethodDelete {
		script = `if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end; redis.call('DEL',KEYS[1]); return 1`
	}
	result, err := redisClient().Eval(c.Request.Context(), script, []string{leaseKey}, body.SessionID).Int()
	if err != nil {
		storageError(c)
	} else if result == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "active_session"})
	} else {
		expired, _ := redisClient().Get(c.Request.Context(), "{esg-monitoring}:expired").Int64()
		c.JSON(http.StatusOK, gin.H{"version": 1, "sessionId": body.SessionID, "leaseSeconds": 90, "expiredDescriptors": expired})
	}
}

func ownsSession(c *gin.Context, id string) bool {
	if !validUUID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_session"})
		return false
	}
	active, err := redisClient().Get(c.Request.Context(), leaseKey).Result()
	if err != nil && err != redis.Nil {
		storageError(c)
		return false
	}
	if active != id {
		c.JSON(http.StatusConflict, gin.H{"error": "active_session"})
		return false
	}
	return true
}

func tasks(c *gin.Context) {
	sessionID := c.Query("sessionId")
	if !ownsSession(c, sessionID) {
		return
	}
	ctx := c.Request.Context()
	client := redisClient()
	result := make([]Descriptor, 0)
	if query := c.Query("esgIds"); query != "" {
		ids := strings.Split(query, ",")
		if len(ids) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "too_many_ids"})
			return
		}
		for _, id := range ids {
			if !validUUID(id) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
				return
			}
		}
		mappings, err := readMappings(ctx, ids)
		if err != nil {
			storageError(c)
			return
		}
		for _, m := range mappings {
			if m.MonitoringSession == "" || m.TaskId == "" {
				continue
			}
			d := Descriptor{SessionID: m.MonitoringSession, Workspace: m.Workspace, EsgID: m.RouterUUID, ECSTaskID: m.TaskId,
				ECSTaskARN: m.MonitoringTaskARN,
				EnvType:    m.MonitoringEnvType, Region: config.Conf.AwsRegion, Cluster: config.Conf.AwsCluster,
				LogsGroup: config.Conf.AwsLogsGroup, LogsPrefix: m.MonitoringEnvType}
			if m.HealthAt != nil {
				d.CreatedAt = *m.HealthAt
			}
			applyMapping(&d, m)
			result = append(result, d)
		}
	} else {
		_, err := client.Eval(ctx, `local n=redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',ARGV[1]); if n>0 then redis.call('INCRBY',KEYS[2],n); redis.call('EXPIRE',KEYS[2],86400) end; redis.call('EXPIRE',KEYS[1],600); return n`, []string{indexKey(sessionID), "{esg-monitoring}:expired"}, time.Now().Add(-5*time.Minute).Unix()).Result()
		if err != nil {
			storageError(c)
			return
		}
		ids, err := client.ZRangeByScore(ctx, indexKey(sessionID), &redis.ZRangeBy{Min: "(" + strconv.FormatInt(time.Now().Add(-5*time.Minute).Unix(), 10), Max: "+inf", Count: 100}).Result()
		if err != nil {
			storageError(c)
			return
		}
		if len(ids) > 0 {
			keys := make([]string, len(ids))
			for i, id := range ids {
				keys[i] = descriptorKey(sessionID, id)
			}
			values, err := client.MGet(ctx, keys...).Result()
			if err != nil {
				storageError(c)
				return
			}
			esgIDs := make([]string, 0, len(values))
			for _, value := range values {
				data, ok := value.(string)
				var d Descriptor
				if ok && json.Unmarshal([]byte(data), &d) == nil {
					result = append(result, d)
					esgIDs = append(esgIDs, d.EsgID)
				}
			}
			mappings, err := readMappings(ctx, esgIDs)
			if err != nil {
				storageError(c)
				return
			}
			for i := range result {
				if m, ok := mappings[result[i].EsgID]; ok && m.TaskId == result[i].ECSTaskID {
					applyMapping(&result[i], m)
				}
			}
		}
	}
	expired, _ := client.Get(ctx, "{esg-monitoring}:expired").Int64()
	c.JSON(http.StatusOK, gin.H{"version": 1, "tasks": result, "expiredDescriptors": expired})
}

func readMappings(ctx context.Context, ids []string) (map[string]cachedTask, error) {
	result := make(map[string]cachedTask)
	if len(ids) == 0 {
		return result, nil
	}
	pipe := redisClient().Pipeline()
	for _, id := range ids {
		pipe.Get(ctx, id)
	}
	commands, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	for _, command := range commands {
		data, err := command.(*redis.StringCmd).Bytes()
		var m cachedTask
		if err == nil && len(data) <= 1024*1024 && json.Unmarshal(data, &m) == nil {
			result[m.RouterUUID] = m
		}
	}
	return result, nil
}

func applyMapping(d *Descriptor, m cachedTask) {
	d.State = "queued"
	if m.Status == 2 {
		d.State = "running"
	} else if m.Status == 3 {
		d.State = "stopped"
	}
	d.StopReason = m.StopReason
	if ip := net.ParseIP(m.Network.IP); ip == nil || !ip.IsPrivate() {
		return
	}
	if endpoint := m.Network.Endpoints["recorderStop"]; endpoint != nil && endpoint.HostPort > 0 {
		if address, ok := m.Network.GetUrl("recorderStop"); ok {
			address.Path = ""
			d.RecorderURL = address.String()
		}
	}
}

func acknowledge(c *gin.Context) {
	var body struct {
		SessionID string   `json:"sessionId"`
		TaskIDs   []string `json:"taskIds"`
	}
	if c.ShouldBindJSON(&body) != nil || len(body.TaskIDs) == 0 || len(body.TaskIDs) > 100 || !validUUID(body.SessionID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_acknowledgment"})
		return
	}
	keys := []string{leaseKey, indexKey(body.SessionID)}
	args := []interface{}{body.SessionID}
	for _, id := range body.TaskIDs {
		if !taskPattern.MatchString(id) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
			return
		}
		keys = append(keys, descriptorKey(body.SessionID, id))
		args = append(args, id)
	}
	result, err := redisClient().Eval(c.Request.Context(), `
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
for i=2,#ARGV do redis.call('ZREM',KEYS[2],ARGV[i]); redis.call('DEL',KEYS[i+1]) end
return 1`, keys, args...).Int()
	if err != nil {
		storageError(c)
	} else if result == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "active_session"})
	} else {
		c.Status(http.StatusNoContent)
	}
}

func storageError(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage_unavailable"})
}
