package network

import (
	"net/url"
	"strconv"
)

type Endpoint struct {
	HostPort      int64
	ContainerPort int64
	Path          string
}

type NetworkConfiguration struct {
	IP        string
	Endpoints map[string]*Endpoint
}

func (n *NetworkConfiguration) GetUrl(endpointName string) (u *url.URL, ok bool) {
	endpoint, ok := n.Endpoints[endpointName]
	if !ok {
		return nil, false
	}

	ip := n.IP
	if ip == "" {
		return nil, false
	}

	host := ip + ":" + strconv.FormatInt(endpoint.HostPort, 10)
	return &url.URL{Scheme: "http", Host: host, Path: endpoint.Path}, true
}
