package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
	"golang.org/x/net/websocket"
)

const (
	playwrightKeepAliveMinInterval = 5 * time.Second
	playwrightKeepAliveMaxInterval = 30 * time.Second
)

func PlaywrightAttach(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	l := log.WithField(config.RouterUUID, mapperEntity.RouterUUID)

	driverUrl, ok := mapperEntity.Network.GetUrl("driver")
	if !ok {
		l.Error("Playwright attach: driver url is not available")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "playwright endpoint is not available"})
		return
	}

	wsServer := websocket.Server{
		// Playwright clients send no Origin header, so skip the default origin check.
		Handshake: func(config *websocket.Config, req *http.Request) error {
			return nil
		},
		Handler: func(clientWS *websocket.Conn) {
			defer clientWS.Close()

			wsTarget := fmt.Sprintf("ws://%s/playwright", driverUrl.Host)
			origin := fmt.Sprintf("http://%s", driverUrl.Host)
			serverWS, err := websocket.Dial(wsTarget, "", origin)
			if err != nil {
				l.WithError(err).Error("Playwright attach: failed to connect to browser")
				return
			}
			defer serverWS.Close()

			keepAliveCtx, stopKeepAlive := context.WithCancel(context.Background())
			defer stopKeepAlive()
			go keepPlaywrightSessionAlive(keepAliveCtx, mapperEntity.RouterUUID, mapperEntity.IdleTimeout, l)

			l.Info("Playwright attach: proxying")
			proxyPlaywrightWS(clientWS, serverWS)
			l.Info("Playwright attach: client disconnected")
		},
	}
	wsServer.ServeHTTP(c.Writer, c.Request)
}

// keepPlaywrightSessionAlive refreshes the access time so the scaler does not abort a connected session.
func keepPlaywrightSessionAlive(ctx context.Context, routerUUID string, idleTimeout float64, l *log.Entry) {
	interval := time.Duration(idleTimeout/3) * time.Second
	if interval < playwrightKeepAliveMinInterval {
		interval = playwrightKeepAliveMinInterval
	} else if interval > playwrightKeepAliveMaxInterval {
		interval = playwrightKeepAliveMaxInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mapperEntity, err := mapper.Find(routerUUID)
			if err != nil || mapperEntity == nil {
				l.WithError(err).Debug("Playwright attach: session is gone, keep-alive stopped")
				return
			}

			if mapperEntity.Status == mapper.Stopped {
				l.Debug("Playwright attach: session is stopped, keep-alive stopped")
				return
			}

			accessedAt := time.Now()
			mapperEntity.AccessedAt = &accessedAt
			if err := mapper.Write(mapperEntity, -1); err != nil {
				l.WithError(err).Warn("Playwright attach: failed to refresh last access time")
			}
		}
	}
}

func proxyPlaywrightWS(clientWS, serverWS *websocket.Conn) {
	errc := make(chan error, 2)
	go func() {
		for {
			var msg []byte
			if err := websocket.Message.Receive(serverWS, &msg); err != nil {
				errc <- err
				return
			}
			if err := websocket.Message.Send(clientWS, msg); err != nil {
				errc <- err
				return
			}
		}
	}()
	go func() {
		for {
			var msg []byte
			if err := websocket.Message.Receive(clientWS, &msg); err != nil {
				errc <- err
				return
			}
			if err := websocket.Message.Send(serverWS, msg); err != nil {
				errc <- err
				return
			}
		}
	}()
	<-errc
}
