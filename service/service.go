package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/selenium"
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
	Url      *url.URL
	HostPort selenium.HostPort
	TaskID   string
	Cancel   func()
}

// Starter - interface to create session with cancellation ability
type Starter interface {
	StartWithCancel(ctx context.Context, username string) (*StartedService, error)
}

// Manager - interface to choose appropriate starter
type Manager interface {
	Find(caps selenium.Caps) (Starter, bool)
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

// Find - default implementation Manager interface
func (m *DefaultManager) Find(caps selenium.Caps) (Starter, bool) {
	browser := caps.BrowserName()
	version := strings.ToLower(caps.Version)

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

	useAsLatest := []string{
		"null",
		"latest",
		"",
	}

	for _, item := range useAsLatest {
		if item == version {
			version = "latest"
			break
		}
	}

	image := fmt.Sprintf("%s/%s:%s", org, browser, version)

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

func wait(ctx context.Context, u string, t time.Duration) error {
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
	case <-ctx.Done():
		close(done)
		return ctx.Err()
	case <-up:
	}
	return nil
}
