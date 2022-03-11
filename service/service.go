package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// StartedService - all started service properties
type StartedService struct {
	Url       *url.URL
	Endpoints map[string]string
	TaskID    string
	Cancel    func()
}

func wait(ctx context.Context, u string, t time.Duration) error {
	up := make(chan struct{})
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			req, _ := http.NewRequest(http.MethodHead, u, nil)
			req.Close = true
			resp, err := http.DefaultClient.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil {
				<-time.After(50 * time.Millisecond)
				continue
			}
			up <- struct{}{}
			return
		}
	}()
	select {
	case <-time.After(t):
		close(done)
		return fmt.Errorf("%s does not respond in %v", u, t)
	case <-ctx.Done():
		close(done)
		return ctx.Err()
	case <-up:
	}
	return nil
}
