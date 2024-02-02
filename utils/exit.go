package utils

import (
	"os"

	log "github.com/sirupsen/logrus"
)

func ExitWithError(err error, message string, l *log.Entry) {
	l.WithField("message", message).WithError(err).Fatal("Stopping container...")
	os.Exit(1)
}
