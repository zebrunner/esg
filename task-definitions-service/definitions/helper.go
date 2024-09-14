package definitions

import (
	sync "sync"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
	grpc "google.golang.org/grpc"
)

type Client struct {
	sync.Mutex
	Client ServiceClient
}

var (
	client = &Client{}
)

// Get gRPC client for interacting with the task definition service
// if cannot connect to service cause os.Exit
func GetClient() ServiceClient {
	defer func() {
		client.Unlock()
	}()
	client.Lock()
	if client.Client != nil {
		return client.Client
	}
	c, err := grpc.NewClient(config.Conf.DefinitionsConnectionString)
	if err != nil {
		utils.ExitWithError(err, "failed to initialize gRPC definitions client", log.NewEntry(log.StandardLogger()))
	}
	client.Client = NewServiceClient(c)
	return client.Client
}
