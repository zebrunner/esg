package cachemaps

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

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

func SendMessageToAllChanns[R interface{}, T chan R](ch T, message R) {
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
