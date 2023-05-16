package main

import (
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/utils"
)

func main() {
	images, err := utils.ListBrowsers()
	if err != nil {
		log.WithError(err).Error("Failed to get image list")
	}

	f, err := os.Create("browsers.txt")
	if err != nil {
		log.WithError(err).Fatal("Failed to create file")
	}
	_, err = f.WriteString(strings.Join(images, "\n"))
	if err != nil {
		log.WithError(err).Fatal("Failed to write file")
	}
	log.Info("File browsers.txt successfully created")
}
