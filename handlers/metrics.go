package handlers

import (
	"fmt"
	"net/http"
	"net/http/httputil"

	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/utils"
)

type metric struct {
	path    string
	address string
}

var (
	metricsAddressMap = make(map[string]*metric)
)

func init() {
	ip, err := utils.GetMetadata(utils.PrivateIpv4Item)
	if err != nil {
		ip, err = utils.GetMetadata(utils.Ipv6Item)
		if err != nil {
			return
		}
	}

	metricsAddressMap["alertmanager"] = &metric{address: fmt.Sprintf("%s:9093", ip), path: "/metrics/alertmanager"}
	metricsAddressMap["prometheus"] = &metric{address: fmt.Sprintf("%s:9090", ip), path: "/metrics/prometheus"}
	metricsAddressMap["grafana"] = &metric{address: fmt.Sprintf("%s:3000", ip), path: "/metrics/grafana"}
}

func ProxyMetrics(c *gin.Context) {
	metric, err := getMetric(c.Request.URL.Path)
	if err != nil {
		log.WithError(err).Error("failed to get metrics url from path")
		c.Error(utils.UnknownApiErr(err.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = metric.address
			r.URL.Path = getRemainingPath(c.Request.URL.Path, 3)
		},
		ModifyResponse: func(r *http.Response) error {
			if strings.Contains(metric.path, "prometheus") {
				locationValue := r.Header.Get("Location")
				if locationValue != "" {
					locationValue = metric.path + locationValue
					r.Header.Set("Location", locationValue)
				}
			}
			return nil
		},
	}).ServeHTTP(c.Writer, c.Request)
}

func getMetric(path string) (*metric, error) {
	splittedPath := strings.Split(path, "/")
	if len(splittedPath) < 3 {
		return nil, fmt.Errorf("metric tool for empty path is not found")
	}

	metric, ok := metricsAddressMap[splittedPath[2]]
	if !ok {
		return nil, fmt.Errorf("metric for '%s' path is not found", splittedPath[1])
	}

	return metric, nil
}
