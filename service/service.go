package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-redis/redis/v8"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/session"
)

// Environment - all settings that influence browser startup
type Environment struct {
	IP                   string
	CPU                  int64
	Memory               int64
	Network              string
	Hostname             string
	StartupTimeout       time.Duration
	SessionDeleteTimeout time.Duration
	CaptureDriverLogs    bool
	VideoOutputDir       string
	VideoContainerImage  string
	LogOutputDir         string
	SaveAllLogs          bool
}

const (
	DefaultContainerNetwork = "default"
)

// ServiceBase - stores fields required by all services
type ServiceBase struct {
	RequestId uint64
	Service   *Browser
}

// StartedService - all started service properties
type StartedService struct {
	Url       *url.URL
	Container *session.Container
	HostPort  session.HostPort
	TaskID    string
	Cancel    func()
}

// Starter - interface to create session with cancellation ability
type Starter interface {
	StartWithCancel(username string) (*StartedService, error)
}

// Manager - interface to choose appropriate starter
type Manager interface {
	Find(caps session.Caps) (Starter, bool)
}

// DefaultManager - struct for default implementation
type DefaultManager struct {
	Environment *Environment
}

// Browser configuration
type Browser struct {
	Image string
	Path  string
	Port  int64
}

func InitCache() (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     config.AwsElasticCache,
		Password: "",
		DB:       0,
	})

	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		return nil, err
	}

	return client, nil
}

// Find - default implementation Manager interface
func (m *DefaultManager) Find(caps session.Caps) (Starter, bool) {
	browser := caps.BrowserName()
	version := caps.Version

	//log.Printf("[LOCATING_SERVICE] [%s] [%s]", browser, version)
	log.WithFields(log.Fields{
		"browser": browser,
		"version": version,
	}).Info("Locating service")

	org := "public.ecr.aws/zebrunner" //public zebrunner ECR docker registry
	if browser == "MicrosoftEdge" {
		browser = "edge"
	}

	if browser == "operablink" {
		browser = "opera"
	}

	if version != "" {
		version = ":" + caps.Version
	}

	image := fmt.Sprintf("%s/%s%s", org, browser, version)

	path := ""
	if browser == "firefox" {
		path = "/wd/hub"
	}

	//TODO: add support for non selenoid images usage
	service := Browser{
		Image: image,
		Path:  path,
		Port:  4444,
	}

	serviceBase := ServiceBase{Service: &service}
	log.WithFields(log.Fields{
		"browser": browser,
		"service": service,
	})
	return &Task{
		ServiceBase: serviceBase,
		Environment: *m.Environment,
		Caps:        caps}, true
}

func wait(u string, t time.Duration) error {
	up := make(chan struct{})
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			req, _ := http.NewRequest(http.MethodHead, u, nil)
			req.Close = true
			resp, err := http.DefaultClient.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil {
				<-time.After(50 * time.Millisecond)
				continue
			}
			up <- struct{}{}
			return
		}
	}()
	select {
	case <-time.After(t):
		close(done)
		return fmt.Errorf("%s does not respond in %v", u, t)
	case <-up:
	}
	return nil
}
