package cachemaps

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/config"
)

type SetType string

func (set SetType) String() string {
	return string(set)
}

const (
	SESSION               SetType = "sessions-set"
	TASK                  SetType = "tasks-set"
	DEFINITION            SetType = "definitions-set"
	UNALLOCATED_RESOURCES SetType = "unallocated-resources-set"
	UTILS                 SetType = "utils-set"
)

func AppendToSet(st SetType, key string) (int64, error) {
	return config.RedisCluster.SAdd(context.Background(), st.String(), key).Result()
}

func RemoveFromSet(st SetType, key string) error {
	return config.RedisCluster.SRem(context.Background(), st.String(), key).Err()
}

func GetKeys(st SetType) ([]string, error) {
	keys := make([]string, 0)

	appendCh := make(chan string)
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
	go func(ctx context.Context, appencCh <-chan string) {
		for {
			select {
			case v := <-appendCh:
				keys = append(keys, v)
			case <-ctx.Done():
				return
			}
		}
	}(ctx, appendCh)

	err := config.RedisCluster.ForEachMaster(context.Background(), func(ctx context.Context, rdb *redis.Client) error {
		keysSet := make(map[string]string)
		iter := rdb.SScan(context.Background(), string(st), 0, "*", 50).Iterator()
		for iter.Next(context.Background()) {
			key := iter.Val()
			keysSet[key] = key
		}

		if err := iter.Err(); err != nil {
			if !strings.Contains(err.Error(), "MOVED") {
				return err
			}
		}

		for key := range keysSet {
			appendCh <- key
		}

		return nil
	})

	return keys, err
}

// Finds all items by passed ids, and tries to unmarshal this object to R type.
// Records, that are not found by passed id, are not present in final array
func FindAll[R interface{}](rdbPipe redis.Pipeliner, ids []string) ([]R, error) {
	for _, id := range ids {
		rdbPipe.Get(context.Background(), id)
	}

	cmds, err := rdbPipe.Exec(context.Background())
	if err != nil {
		return nil, err
	}

	items := make([]R, 0)
	for _, cmd := range cmds {
		data, err := cmd.(*redis.StringCmd).Result()
		if err != nil {
			continue
		}

		var item R
		err = json.Unmarshal([]byte(data), &item)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, nil
}

// Writes all items with Type R to redis db (to which was created rdbPipe) with the same expiration time for every record.
func WriteAll[R interface{}](rdbPipe redis.Pipeliner, st SetType, items map[string]R) error {
	for key, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		rdbPipe.Set(context.Background(), key, data, 0)
		rdbPipe.SAdd(context.Background(), st.String(), key)
	}
	_, err := rdbPipe.Exec(context.Background())
	return err
}

func ExpireAll(rdbPipe redis.Pipeliner, st SetType, items []string, expiration time.Duration) error {
	for _, key := range items {
		rdbPipe.Expire(context.Background(), key, expiration)
		rdbPipe.SRem(context.Background(), st.String(), key)
	}
	_, err := rdbPipe.Exec(context.Background())
	return err
}

func WriteWithExpire[R interface{}](rdbPipe redis.Pipeliner, st SetType, items map[string]R, expiration time.Duration) error {
	for key, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		rdbPipe.Set(context.Background(), key, data, expiration)
		rdbPipe.SRem(context.Background(), st.String(), key)
	}
	_, err := rdbPipe.Exec(context.Background())
	return err
}

type workerExecutFunc[T interface{}] func(map[string]T) error

type RedisWorker[T interface{}] struct {
	mutex sync.Mutex
	// function, that will be executed every iteration if requests are present
	executFunc workerExecutFunc[T]
	// request items. key -> id of a record in redis, value -> new record by specified key
	items map[string]T
	// signals that work is done. If nil -> successfully executed, else -> something went wrong.
	errCh chan error
}

// Copies error chan, that would recieve a reponse on the next worker iteration.
// Adds request item to worker and waits for reponse from err channel.
func (w *RedisWorker[T]) AppendToWorker(id string, item T) error {
	w.mutex.Lock()
	w.items[id] = item
	errCh := w.errCh
	w.mutex.Unlock()
	return <-errCh
}

// Worker begins to execute w.executFunc() every iterationPause time. Start() should be called in a new thread
func (w *RedisWorker[T]) Start(iterationPause time.Duration) {
	for {
		// get current state of a worker to new vars and flush all fields,
		// so new requests would wait for a new iteration of a worker and have a new err channels
		time.Sleep(iterationPause)
		w.mutex.Lock()
		items := w.items
		errCh := w.errCh
		w.flush()
		w.mutex.Unlock()

		if len(items) == 0 {
			continue
		}

		err := w.executFunc(items)

		SendMessageToAllChannels(errCh, err)
	}
}

// Create new objects and assign them to the worker fields. Old objects are no longer accessible from worker instance
func (w *RedisWorker[T]) flush() {
	w.items = make(map[string]T, 0)
	w.errCh = make(chan error)
}

// Creates a new worker by specifying item type (T), redisClient (redis db), and function, that will be executed iteratively
func CreateRedisWorker[T interface{}](executeFunc workerExecutFunc[T]) RedisWorker[T] {
	return RedisWorker[T]{
		mutex:      sync.Mutex{},
		executFunc: executeFunc,
		items:      make(map[string]T),
		errCh:      make(chan error),
	}
}

// Sending specified message to chan ch, until no more listeners are waiting on this chan
func SendMessageToAllChannels[R interface{}, T chan R](ch T, message R) {
	for {
		done := false
		select {
		// keep sending before no waiting receiver is left
		case ch <- message:
		default:
			done = true
		}

		if done {
			break
		}
	}
}
