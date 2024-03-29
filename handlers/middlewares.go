package handlers

import (
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/utils"
)

func APIError(c *gin.Context) {
	c.Next()
	if c.Errors.Last() == nil {
		return
	}

	for _, err := range c.Errors {
		log.WithFields(log.Fields{
			"client": c.ClientIP(),
		}).WithError(err).Warn("API error received")
	}

	var apiErr *utils.APIError

	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		passedApiErr, ok := publicError.Err.(*utils.APIError)
		if ok {
			apiErr = passedApiErr
		}
	}

	if apiErr == nil {
		log.Debug("APIError(): intercepted error is either not public or not Api Error type. Setting default values...")
		apiErr = utils.UnknownApiErr("internal server error")
	}

	log.WithFields(log.Fields{
		"client":   c.ClientIP(),
		"status":   apiErr.Status,
		"response": apiErr.Message,
	}).Warn("Error response response")

	apiErr.SendEncodedResponse(c)
}

func SeleniumError(c *gin.Context) {
	c.Next()

	if c.Errors.Last() == nil {
		return
	}

	l := log.NewEntry(log.StandardLogger())
	enableDebug := true

	if mapperEntity, ok := c.Get(config.RouterUUID); ok {
		if m, ok := mapperEntity.(*mapper.Mapper); ok {
			// Capabilities.EnableDebug by default - false
			enableDebug = m.Capabilities.EnableDebug.ToPrimitive()
			l = l.WithField(config.RouterUUID, m.RouterUUID)
			if m.TaskId != "" {
				l = l.WithField(config.TaskIdKey, m.TaskId)
			}
			if m.SessionID != "" {
				l = l.WithField(config.SessionIdKey, m.SessionID)
			}
		}
	}

	for _, err := range c.Errors {
		l.WithError(err).Debug("Selenium error received")
	}

	var seErr *utils.SeleniumError

	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		passedSeErr, ok := publicError.Err.(*utils.SeleniumError)
		if ok {
			seErr = passedSeErr
		}
	}

	if seErr == nil {
		l.Debug("SeleniumError(): intercepted error is either not public or not Selenium type error. Setting default values...")
		seErr = utils.UnknownErr(fmt.Errorf("internal server error"))
	}

	l.WithFields(log.Fields{
		"status":  seErr.ResponseStatus,
		"error":   seErr.Error(),
		"debug":   enableDebug,
		"request": fmt.Sprintf("%s: %s", c.Request.Method, c.Request.URL.Path),
	}).Warn("Error sent to selenium")

	seErr.SendEncodedResponse(c, enableDebug)
}

func ValidateGenericMapperPresence(c *gin.Context) {
	uuid := c.Param("uuid")

	var seErr *utils.SeleniumError
	mapperEntity, err := mapper.Find(uuid, false)

	if err != nil || mapperEntity == nil {
		seErr = utils.NoSuchSessionErr(fmt.Errorf("session timed out or not found"))
	} else if mapperEntity.Status == mapper.Stopped {
		seErr = utils.SessionStoppedErr(fmt.Errorf(string(mapperEntity.StopReason)))
	}

	if seErr != nil {
		log.WithField(config.RouterUUID, uuid).WithError(seErr).Error("can't access session")

		c.Error(seErr).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	c.Set(config.RouterUUID, mapperEntity)
	c.Next()
}

func ValidateMapperPresence(c *gin.Context) {
	uuid := c.Param("uuid")

	var seErr *utils.SeleniumError
	mapperEntity, err := mapper.Find(uuid, false)
	if err != nil || mapperEntity == nil {
		seErr = utils.NoSuchSessionErr(fmt.Errorf("session timed out or not found"))
	} else if mapperEntity.Status == mapper.Queued {
		seErr = utils.NoSuchSessionErr(fmt.Errorf("session creation is in queue"))
	} else if mapperEntity.Status == mapper.Stopped {
		seErr = utils.SessionStoppedErr(fmt.Errorf(string(mapperEntity.StopReason)))
	}

	if seErr != nil {
		log.WithField(config.RouterUUID, uuid).WithError(seErr).Error("can't access session")

		c.Error(seErr).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	c.Set(config.RouterUUID, mapperEntity)
	c.Next()
}

// Must be used only after ValidateMapperPresence() func
func ValidateSessionStatus(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	if mapperEntity.SessionID == "" {
		seErr := utils.NoSuchSessionErr(fmt.Errorf("session not found"))
		c.Error(seErr).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	c.Next()
}

func APIAuthentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")

		c.Error(utils.AuthApiErr("auth credentials not found")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	apiErr := db.CheckAuth(username, password)
	if apiErr != nil {
		log.WithError(apiErr).WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")

		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}
}

func LowLvlAuthentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")

		c.Error(utils.AuthApiErr("auth credentials not found")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	if username != "root" || password != os.Getenv("API_ACCESS_KEY") {
		log.WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")

		c.Error(utils.AuthApiErr("provided credentials not valid")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}
}

// #1056: Small chance of double shaping on generic task abort
func LockGenericTaskCache(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)

	for {
		if ok := utilsmap.AcquireLock(mapperEntity.RouterUUID, 0); ok {
			break
		}
		time.Sleep(10 * time.Second)
	}

	c.Next()

	err := utilsmap.ReleaseLock(mapperEntity.RouterUUID)
	if err != nil {
		log.WithField(config.RouterUUID, mapperEntity.RouterUUID).WithError(err).Error("Failed to release lock for mapper cache!")
	}
}
