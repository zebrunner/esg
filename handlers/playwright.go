package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/playwright"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/utils"
	"golang.org/x/net/websocket"
)

const (
	playwrightKeepAliveMinInterval = 5 * time.Second
	playwrightKeepAliveMaxInterval = 30 * time.Second

	playwrightRefreshTimeout = 2 * time.Minute

	childSessionGrace = 30 * time.Minute
)

type playwrightRefreshRequest struct {
	BrowserName    string  `json:"browserName"`
	PlaywrightArgs *string `json:"playwrightArgs"`
	Headless       *bool   `json:"headless"`
}

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

// PlaywrightRefresh swaps the browser inside the running task and reopens artifact collection.
func PlaywrightRefresh(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	l := log.WithField(config.RouterUUID, mapperEntity.RouterUUID)

	if !isPlaywrightSession(mapperEntity) {
		c.Error(utils.InvalidArgErr(fmt.Errorf("refresh is supported for playwright sessions only"))).SetType(gin.ErrorTypePublic)
		return
	}

	var req playwrightRefreshRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.Error(utils.InvalidArgErr(fmt.Errorf("failed to parse request body"), err.Error())).SetType(gin.ErrorTypePublic)
			return
		}
	}

	// An empty browser name restarts the current engine, which Playwright gives a clean profile.
	browserType := ""
	if req.BrowserName != "" {
		resolved, err := environment.ResolvePlaywrightBrowserType(req.BrowserName)
		if err != nil {
			c.Error(utils.InvalidArgErr(err)).SetType(gin.ErrorTypePublic)
			return
		}
		browserType = resolved
	}

	// One id for the client, the recorder and the artifact scope of the browser about to start.
	childUUID := uuid.NewString()
	l = l.WithField(config.ChildUUIDKey, childUUID)

	// A swap can outlast the idle timeout while no client is attached, so hold the session open.
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	go keepPlaywrightSessionAlive(heartbeatCtx, mapperEntity.RouterUUID, mapperEntity.IdleTimeout, l)

	// Publish the current artifacts before the swap so each browser owns its own scope.
	rotation, err := selenium.RotateRecording(&mapperEntity.Network, childUUID)
	if err != nil {
		l.WithError(err).Error("Playwright refresh: failed to rotate artifacts")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to rotate artifacts"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	// Rotating stopped the recorder, so a failure from here on must put it back or the rest of the
	// session records nothing.
	recordingResumed := false
	defer func() {
		if recordingResumed {
			return
		}
		if err := selenium.StartRecording(&mapperEntity.Network); err != nil {
			l.WithError(err).Error("Playwright refresh: failed to resume recording after a failed refresh")
		}
	}()

	_, err = playwright.Refresh(&mapperEntity.Network, playwright.RefreshOptions{
		BrowserType: browserType,
		Args:        req.PlaywrightArgs,
		Headless:    req.Headless,
	})
	if err != nil {
		l.WithError(err).Error("Playwright refresh: failed to refresh browser")
		c.Error(playwrightRefreshErr(err)).SetType(gin.ErrorTypePublic)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), playwrightRefreshTimeout)
	defer cancel()

	state, err := playwright.WaitReady(ctx, &mapperEntity.Network)
	if err != nil {
		l.WithError(err).Error("Playwright refresh: browser did not become ready")
		c.Error(utils.UnknownErr(fmt.Errorf("browser did not become ready"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	stopHeartbeat()

	// The browser is already usable, so a recorder failure must not fail the refresh.
	if err := selenium.StartRecording(&mapperEntity.Network); err != nil {
		l.WithError(err).Error("Playwright refresh: failed to start recording")
	}
	recordingResumed = true

	// An unregistered child id resolves nowhere, so fall back to the id that still routes.
	sessionID := childUUID
	if err := persistRefreshedSession(mapperEntity, req, childUUID); err != nil {
		l.WithError(err).Error("Playwright refresh: failed to register new session, returning original")
		sessionID = mapperEntity.RouterUUID
	}

	l.WithFields(log.Fields{
		"browserType":        state.BrowserType,
		"generation":         state.Generation,
		"artifactId":         rotation.ArtifactID,
		"previousArtifactId": rotation.PreviousArtifactID,
	}).Info("Playwright refresh: browser replaced")

	c.JSON(http.StatusOK, gin.H{"value": gin.H{
		"sessionId":          sessionID,
		"originalSessionId":  mapperEntity.RouterUUID,
		"artifactId":         rotation.ArtifactID,
		"previousArtifactId": rotation.PreviousArtifactID,
		"browserType":        state.BrowserType,
		"generation":         state.Generation,
	}})
}

// A swap can outlive the idle timeout, so the record read before it must not be written back as is.
// persistRefreshedSession registers childUUID and applies the request to the freshest session record.
func persistRefreshedSession(fallback *mapper.Mapper, req playwrightRefreshRequest, childUUID string) error {
	// The child id must resolve before it is advertised, so it is written ahead of the session record.
	if err := mapper.WriteChild(childUUID, fallback.RouterUUID, childSessionTTL()); err != nil {
		return err
	}

	entity, err := mapper.Find(fallback.RouterUUID)
	if err != nil || entity == nil || entity.Capabilities == nil {
		entity = fallback
	}

	if req.BrowserName != "" {
		entity.Capabilities.BrowserName.From(req.BrowserName)
	}
	if req.PlaywrightArgs != nil {
		entity.Capabilities.PlaywrightArgs.From(*req.PlaywrightArgs)
	}
	if req.Headless != nil {
		entity.Capabilities.Headless.From(*req.Headless)
	}

	if !slices.Contains(entity.Children, childUUID) {
		entity.Children = append(entity.Children, childUUID)
	}

	accessedAt := time.Now()
	entity.AccessedAt = &accessedAt

	return mapper.Write(entity, -1)
}

// A child id outlives its session because the scaler caps every task at MaxTimeout, so it self-expires
// instead of relying on a stop path that an aborted or lost task would never reach.
func childSessionTTL() time.Duration {
	return config.Conf.MaxTimeout + childSessionGrace
}

func isPlaywrightSession(mapperEntity *mapper.Mapper) bool {
	if mapperEntity.Capabilities == nil {
		return false
	}

	return strings.EqualFold(mapperEntity.Capabilities.PlatformName.ToPrimitive(), envtype.PLAYWRIGHT.String())
}

func playwrightRefreshErr(err error) *utils.SeleniumError {
	var controlErr *playwright.ControlError
	if errors.As(err, &controlErr) && controlErr.StatusCode == http.StatusConflict {
		return &utils.SeleniumError{
			ResponseStatus: http.StatusConflict,
			Name:           "browser refresh in progress",
			MainErr:        fmt.Errorf("another refresh is already running for this session"),
		}
	}

	return utils.UnknownErr(fmt.Errorf("failed to refresh browser"), err.Error())
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
