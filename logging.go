package main

import (
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func TraceLogFromating(param gin.LogFormatterParams) string {
	log.WithFields(log.Fields{
		"code":    param.StatusCode,
		"latency": param.Latency,
		"client":  param.ClientIP,
		"method":  param.Method,
		"path":    param.Path,
	}).Info()
	return ""
}
