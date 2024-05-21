package selenium

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/utils"
)

var (
	httpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type startSessRequest struct {
	EssentialErrCh    chan error
	NonEssentialErrCh chan error
	ResponseCh        chan map[string]interface{}
}

func startSession(ctx context.Context, env *environment.ExecutionEnvironment, sessReq startSessRequest) {
	reqUrl, ok := env.Network.GetUrl("driver")
	if !ok {
		utils.SendToChanIfNotBlocked(sessReq.NonEssentialErrCh, fmt.Errorf("failed to get driver network"))
		return
	}

	reqUrl.Path = path.Join(reqUrl.Path, "/session")

	body, err := env.ReqCapabilities.ToRequestBody()
	if err != nil {
		utils.SendToChanIfNotBlocked(sessReq.EssentialErrCh, err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, reqUrl.String(), body)
	if err != nil {
		utils.SendToChanIfNotBlocked(sessReq.EssentialErrCh, err)
		return
	}

	req.Method = http.MethodPost
	req.Host = "localhost"
	req = req.WithContext(ctx)

	resp, err := httpClient.Do(req)
	if err != nil {
		utils.SendToChanIfNotBlocked(sessReq.EssentialErrCh, err)
		return
	}

	defer resp.Body.Close()
	var reply map[string]interface{}

	err = json.NewDecoder(resp.Body).Decode(&reply)
	if err != nil {
		utils.SendToChanIfNotBlocked(sessReq.EssentialErrCh, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest {
			utils.SendToChanIfNotBlocked(sessReq.EssentialErrCh, err)
		} else {
			utils.SendToChanIfNotBlocked(sessReq.NonEssentialErrCh, fmt.Errorf("%v", reply))
		}
		return
	}

	go func() {
		err := startRecording(env.Network)
		if err != nil {
			log.WithError(err).Error("Failed to start recording")
		}
	}()

	utils.SendToChanIfNotBlocked(sessReq.ResponseCh, reply)
}

func WaitForSessionStart(ctx context.Context, env *environment.ExecutionEnvironment) *startSessRequest {
	sessReq := startSessRequest{
		EssentialErrCh:    make(chan error),
		NonEssentialErrCh: make(chan error),
		ResponseCh:        make(chan map[string]interface{}),
	}

	go startSession(ctx, env, sessReq)

	return &sessReq
}

func CloseSession(mapperEntity *mapper.Mapper) {
	l := log.WithFields(log.Fields{config.TaskIdKey: mapperEntity.TaskId, config.SessionIdKey: mapperEntity.SessionID, config.RouterUUID: mapperEntity.RouterUUID})
	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", mapperEntity.Workspace)
	}

	conf := &config.Conf

	go func() {
		err := stopRecording(&mapperEntity.Network)
		if err != nil {
			log.WithError(err).Error("Failed to start recording")
		}
	}()

	sessionUrl, ok := mapperEntity.Network.GetUrl("driver")
	if ok {
		sessionUrl.Path = sessionUrl.Path + fmt.Sprintf("session/%s", mapperEntity.SessionID)
		timeoutCtx, cancel := context.WithTimeout(context.Background(), conf.SessionDeleteTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(timeoutCtx, http.MethodDelete, sessionUrl.String(), nil)
		if err != nil {
			l.WithError(err).Error("Failed to create request")
			return
		}
		req.Host = "localhost"

		l.WithFields(log.Fields{"method": req.Method, "url": req.URL}).Debug("closing driver")
		resp, err := httpClient.Do(req)
		if err != nil {
			l.WithError(err).Error("Failed to close driver")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			l.WithField("statusCode", resp.Status).Error("Cancel request returned not success status code")
			return
		}
	} else {
		l.Error("failed to get driver url")
	}

	l.Debug("driver closed")
}
