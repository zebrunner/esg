package cachemaps

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type workerExecutFunc[T interface{}] func(*redis.Conn, map[string]T) error

type RedisWorker[T interface{}] struct {
	// used only one connection per worker
	// this connection should not be used simultaneously for different redis operations
	rdsConn *redis.Conn
	mutex   sync.Mutex
	// function, that will be executed every iteration if requests are present
	executFunc workerExecutFunc[T]
	// request items. key -> id of a record in redis, value -> new record by specified key
	items map[string]T
	// signals that work is done. If nil -> successfully executed, else -> something went wrong.
	errCh chan error
}

// Copies error chan, that would recieve a reponse on the next worker iteration.
// Adds request item to worker and waits for reponse from any channel.
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

		err := w.executFunc(w.rdsConn, items)

		SendMessageToAllChannels(errCh, err)
	}
}

// Create new objects and assign them to the worker fields. Old objects are no longer accessible from worker instance
func (w *RedisWorker[T]) flush() {
	w.items = make(map[string]T, 0)
	w.errCh = make(chan error)
}

// Creates a new worker by specifying item type (T), redisClient (redis db), and function, that will be executed iteratively
func CreateRedisWorker[T interface{}](redisClient *redis.Client, executeFunc workerExecutFunc[T]) RedisWorker[T] {
	return RedisWorker[T]{
		rdsConn:    redisClient.Conn(),
		mutex:      sync.Mutex{},
		executFunc: executeFunc,
		items:      make(map[string]T),
		errCh:      make(chan error),
	}
}

// Writes all items with Type R to redis db (to which was created rdbPipe) with the same expiration time for every record.
func WriteAll[R interface{}](rdbPipe redis.Pipeliner, items map[string]R, expiration time.Duration) error {
	for key, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		rdbPipe.Set(context.Background(), key, data, expiration)
	}
	_, err := rdbPipe.Exec(context.Background())
	return err
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
