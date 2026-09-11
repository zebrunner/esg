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
	"github.com/zebrunner/esg/cachemaps/utilsmap"
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
	playwrightRefreshLockTTL = 5 * time.Minute
	playwrightLockReleaseTTL = 5 * time.Second

	childSessionGrace = 30 * time.Minute

	playwrightRefreshLockPrefix = "playwright-refresh-lock:"
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

	lockOwner := uuid.NewString()
	lockKey := playwrightRefreshLockPrefix + mapperEntity.RouterUUID
	locked, err := utilsmap.AcquireExpiringLock(c.Request.Context(), lockKey, lockOwner, playwrightRefreshLockTTL)
	if err != nil {
		l.WithError(err).Error("Playwright refresh: failed to acquire refresh lock")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to acquire refresh lock"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	if !locked {
		c.Error(playwrightRefreshConflictErr()).SetType(gin.ErrorTypePublic)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), playwrightLockReleaseTTL)
		defer cancel()
		if err := utilsmap.ReleaseExpiringLock(ctx, lockKey, lockOwner); err != nil {
			l.WithError(err).Warn("Playwright refresh: failed to release refresh lock")
		}
	}()

	// A swap can outlast the idle timeout while no client is attached, so hold the session open.
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	go keepPlaywrightSessionAlive(heartbeatCtx, mapperEntity.RouterUUID, mapperEntity.IdleTimeout, l)

	ctx, cancel := context.WithTimeout(c.Request.Context(), playwrightRefreshTimeout)
	defer cancel()

	// Replace the browser first. A rejected browser configuration must not rotate the artifact scope.
	state, err := playwright.Refresh(ctx, &mapperEntity.Network, playwright.RefreshOptions{
		BrowserType: browserType,
		Args:        req.PlaywrightArgs,
		Headless:    req.Headless,
	})
	if err != nil {
		l.WithError(err).Error("Playwright refresh: failed to refresh browser")
		c.Error(playwrightRefreshErr(err)).SetType(gin.ErrorTypePublic)
		return
	}

	childUUID := uuid.NewString()
	l = l.WithField(config.ChildUUIDKey, childUUID)
	sessionID := mapperEntity.RouterUUID
	artifactRotationSucceeded := false
	var rotation *selenium.RotateResult

	// Register the route before the recorder can publish artifacts under the new id.
	if err := mapper.WriteChild(childUUID, mapperEntity.RouterUUID, childSessionTTL()); err != nil {
		l.WithError(err).Error("Playwright refresh: failed to register child session; artifacts remain in the current scope")
		childUUID = ""
	} else {
		sessionID = childUUID
		rotation, err = selenium.RotateRecording(&mapperEntity.Network, childUUID)
		if err != nil {
			l.WithError(err).Error("Playwright refresh: failed to rotate artifacts")
		} else {
			artifactRotationSucceeded = true
		}

		// Rotate stops the recorder. This call also repairs a recorder that returned an uncertain result.
		if err := selenium.StartRecording(&mapperEntity.Network); err != nil {
			l.WithError(err).Error("Playwright refresh: failed to start recording")
		}
	}

	stopHeartbeat()

	if err := persistRefreshedSession(mapperEntity, req, childUUID); err != nil {
		l.WithError(err).Error("Playwright refresh: failed to persist refreshed session state")
	}

	fields := log.Fields{
		"browserType":      state.BrowserType,
		"generation":       state.Generation,
		"artifactRotation": artifactRotationSucceeded,
	}
	value := gin.H{
		"sessionId":                 sessionID,
		"originalSessionId":         mapperEntity.RouterUUID,
		"artifactRotationSucceeded": artifactRotationSucceeded,
		"browserType":               state.BrowserType,
		"generation":                state.Generation,
	}
	if rotation != nil {
		fields["artifactId"] = rotation.ArtifactID
		fields["previousArtifactId"] = rotation.PreviousArtifactID
		value["artifactId"] = rotation.ArtifactID
		value["previousArtifactId"] = rotation.PreviousArtifactID
	}
	if childUUID == "" {
		value["warning"] = "browser replaced, but the child session and artifact scope could not be created"
	} else if !artifactRotationSucceeded {
		value["warning"] = "browser replaced, but artifact rotation did not complete"
	}
	l.WithFields(fields).Info("Playwright refresh: browser replaced")

	c.JSON(http.StatusOK, gin.H{"value": value})
}

// A swap can outlive the idle timeout, so the record read before it must not be written back as is.
// persistRefreshedSession registers childUUID and applies the request to the freshest session record.
func persistRefreshedSession(rootSession *mapper.Mapper, req playwrightRefreshRequest, childUUID string) error {
	return mapper.UpdateSession(rootSession.RouterUUID, func(entity *mapper.Mapper) error {
		if entity.Capabilities == nil {
			return fmt.Errorf("session capabilities are not available")
		}
		if entity.Status == mapper.Stopped {
			return fmt.Errorf("session is stopped")
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

		if childUUID != "" && !slices.Contains(entity.Children, childUUID) {
			entity.Children = append(entity.Children, childUUID)
		}

		accessedAt := time.Now()
		entity.AccessedAt = &accessedAt
		return nil
	})
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
		return playwrightRefreshConflictErr()
	}

	return utils.UnknownErr(fmt.Errorf("failed to refresh browser"), err.Error())
}

func playwrightRefreshConflictErr() *utils.SeleniumError {
	return &utils.SeleniumError{
		ResponseStatus: http.StatusConflict,
		Name:           "browser refresh in progress",
		MainErr:        fmt.Errorf("another refresh is already running for this session"),
	}
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
			updated, err := mapper.UpdateAccessedAt(routerUUID, time.Now())
			if err != nil {
				l.WithError(err).Debug("Playwright attach: session is gone, keep-alive stopped")
				return
			}
			if !updated {
				l.Debug("Playwright attach: session is stopped, keep-alive stopped")
				return
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
