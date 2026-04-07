package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/starter"
	"golang.org/x/net/websocket"
)

func PlaywrightHub(c *gin.Context) {
	l := log.WithField("remote", c.ClientIP())

	capsJSON := c.Request.Header.Get("X-Zebrunner-Capabilities")
	if capsJSON == "" {
		l.Warn("Playwright hub: no X-Zebrunner-Capabilities header")
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-Zebrunner-Capabilities header required"})
		return
	}

	user, password, ok := c.Request.BasicAuth()
	if !ok {
		l.Warn("Playwright hub: no credentials")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authorization required"})
		return
	}

	l.Info("Playwright hub: incoming browser request")

	wsServer := websocket.Server{
		Handshake: func(config *websocket.Config, req *http.Request) error {
			return nil
		},
		Handler: func(clientWS *websocket.Conn) {
			defer clientWS.Close()

			sessionMapper, sessionLog, err := createPlaywrightSession(user, password, capsJSON, c, l)
			if err != nil {
				l.WithError(err).Error("Playwright hub: session creation failed")
				return
			}

			defer func() {
				stopPlaywrightRecording(sessionMapper, sessionLog)
				if err := service.StopTask(context.Background(), *sessionMapper, mapper.TaskFinished); err != nil {
					sessionLog.WithError(err).Warn("Playwright hub: failed to stop task")
				}
				sessionLog.Info("Playwright hub: session closed")
			}()

			startPlaywrightRecording(sessionMapper, sessionLog)

			driverUrl, ok := sessionMapper.Network.GetUrl("driver")
			if !ok {
				sessionLog.Error("Playwright hub: driver url not available")
				return
			}

			wsTarget := fmt.Sprintf("ws://%s/playwright", driverUrl.Host)
			origin := fmt.Sprintf("http://%s", driverUrl.Host)
			serverWS, err := websocket.Dial(wsTarget, "", origin)
			if err != nil {
				sessionLog.WithError(err).Error("Playwright hub: failed to connect to browser")
				return
			}
			defer serverWS.Close()

			sessionLog.Info("Playwright hub: proxying")
			proxyPlaywrightWS(clientWS, serverWS)
			sessionLog.Info("Playwright hub: worker disconnected")
		},
	}
	wsServer.ServeHTTP(c.Writer, c.Request)
}

func createPlaywrightSession(user, password, capsJSON string, c *gin.Context, l *log.Entry) (*mapper.Mapper, *log.Entry, error) {
	apiErr := db.CheckAuth(user, password)
	if apiErr != nil {
		return nil, nil, fmt.Errorf("auth failed: %w", apiErr)
	}

	workspace, err := db.GetWorkspace(user)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace not found: %w", err)
	}

	reqCaps, configCaps, err := capabilities.ParseRequestCapabilities(io.NopCloser(strings.NewReader(capsJSON)))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse capabilities: %w", err)
	}

	env, routerUUID, err := environment.BuildEnvForTaskDefinitionOverride(workspace, configCaps)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build environment: %w", err)
	}

	sessionLog := l.WithField("sessionUUID", routerUUID).WithField("family", env.TaskDefinitionFamily)
	sessionLog.Info("Playwright hub: creating session")

	startupCtx, cancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer cancel()

	resp, seErr := starter.GetServiceStarter(env, workspace, routerUUID, reqCaps, c, sessionLog).StartService(startupCtx)
	if seErr != nil {
		return nil, nil, fmt.Errorf("session startup failed: %s", seErr.Error())
	}

	taskId, _ := resp["taskId"].(string)
	if taskId == "" {
		return nil, nil, fmt.Errorf("no taskId in starter response")
	}

	sessionMapper, err := mapper.Find(taskId)
	if err != nil || sessionMapper == nil {
		return nil, nil, fmt.Errorf("session mapper not found for %s", taskId)
	}

	sessionLog.Info("Playwright hub: session ready")
	return sessionMapper, sessionLog, nil
}

func recorderRequest(method string, endpoint string, m *mapper.Mapper) error {
	recorderUrl, ok := m.Network.GetUrl(endpoint)
	if !ok {
		return fmt.Errorf("recorder endpoint %s not found", endpoint)
	}

	req, err := http.NewRequest(method, recorderUrl.String(), nil)
	if err != nil {
		return err
	}
	req.Host = "localhost"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("recorder %s returned %d", endpoint, resp.StatusCode)
	}
	return nil
}

func startPlaywrightRecording(m *mapper.Mapper, l *log.Entry) {
	if err := recorderRequest(http.MethodPost, "recorderStart", m); err != nil {
		l.WithError(err).Warn("Playwright hub: failed to start recorder")
		return
	}
	l.Info("Playwright hub: recorder started")
}

func stopPlaywrightRecording(m *mapper.Mapper, l *log.Entry) {
	if err := recorderRequest(http.MethodDelete, "recorderFinish", m); err != nil {
		l.WithError(err).Warn("Playwright hub: failed to finish recording")
		if err := recorderRequest(http.MethodDelete, "recorderStop", m); err != nil {
			l.WithError(err).Warn("Playwright hub: failed to stop recording")
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
