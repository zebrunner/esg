package monitoring

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

const leaseKey = "{esg-monitoring}:lease"

var (
	connectionOnce sync.Once
	connection     *redis.ClusterClient
)

type Descriptor struct {
	SessionID   string    `json:"sessionId"`
	Workspace   string    `json:"workspace"`
	EsgID       string    `json:"esgId"`
	ECSTaskID   string    `json:"ecsTaskId"`
	ECSTaskARN  string    `json:"ecsTaskArn"`
	EnvType     string    `json:"envType"`
	Region      string    `json:"region"`
	Cluster     string    `json:"cluster"`
	LogsGroup   string    `json:"logsGroup"`
	LogsPrefix  string    `json:"logsPrefix"`
	RecorderURL string    `json:"recorderUrl,omitempty"`
	State       string    `json:"state"`
	StopReason  string    `json:"stopReason,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

func redisClient() *redis.ClusterClient {
	if !config.Conf.MonitoringCompatibilityEnabled || config.RedisCluster == nil {
		return nil
	}
	connectionOnce.Do(func() {
		options := *config.RedisCluster.Options()
		options.ContextTimeoutEnabled = true
		options.DialTimeout = 500 * time.Millisecond
		options.ReadTimeout = 500 * time.Millisecond
		options.WriteTimeout = 500 * time.Millisecond
		options.PoolTimeout = 500 * time.Millisecond
		options.PoolSize = 2
		options.MaxRetries = -1
		options.MaxRedirects = 1
		connection = redis.NewClusterClient(&options)
	})
	return connection
}

func Close() {
	if connection != nil {
		connection.Close()
	}
}

func Admit() string {
	client := redisClient()
	if client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	session, err := client.Get(ctx, leaseKey).Result()
	if err != nil || !validUUID(session) {
		return ""
	}
	return session
}

func TaskToken(workspace, esgID string) string {
	message, _ := json.Marshal([]string{"v1", workspace, esgID})
	mac := hmac.New(sha256.New, []byte(config.Conf.MonitoringIntegrationToken))
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

func indexKey(session string) string { return "{esg-monitoring}:pending:" + session }
func descriptorKey(session, task string) string {
	return "{esg-monitoring}:task:" + session + ":" + task
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func taskID(arn string) string { return arn[strings.LastIndex(arn, "/")+1:] }

func Publish(d Descriptor) {
	client := redisClient()
	if client == nil || d.SessionID == "" {
		return
	}
	d.ECSTaskID = taskID(d.ECSTaskARN)
	d.CreatedAt = time.Now().UTC()
	d.State = "queued"
	d.Region = config.Conf.AwsRegion
	d.Cluster = config.Conf.AwsCluster
	d.LogsGroup = config.Conf.AwsLogsGroup
	d.LogsPrefix = d.EnvType
	data, err := json.Marshal(d)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = client.Eval(ctx, `
if redis.call('GET',KEYS[1]) ~= ARGV[1] then return 0 end
local expired=redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',ARGV[4]-300)
if expired>0 then redis.call('INCRBY',KEYS[4],expired) end
redis.call('SET',KEYS[3],ARGV[2],'EX',300)
redis.call('ZADD',KEYS[2],ARGV[4],ARGV[3])
local excess=redis.call('ZCARD',KEYS[2])-5000
if excess>0 then
 redis.call('ZREMRANGEBYRANK',KEYS[2],0,excess-1)
 redis.call('INCRBY',KEYS[4],excess)
end
redis.call('EXPIRE',KEYS[2],600)
redis.call('EXPIRE',KEYS[4],86400)
return 1`, []string{leaseKey, indexKey(d.SessionID), descriptorKey(d.SessionID, d.ECSTaskID), "{esg-monitoring}:expired"},
		d.SessionID, string(data), d.ECSTaskID, time.Now().Unix()).Result()
	if err != nil {
		log.WithField("ecsTaskId", d.ECSTaskID).Warn("Task monitoring discovery unavailable")
	}
}
