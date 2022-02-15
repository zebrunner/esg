package main

import (
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/service"
)

func main() {
	s, err := service.InitAws()
	s.Config.Region = aws.String("us-east-1")
	if err != nil {
		log.WithError(err).Fatal("Failed to init aws session")
	}
	service.AwsSess = s
	images, err := service.ListBrowsers()
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
