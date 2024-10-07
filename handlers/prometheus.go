package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	// "path"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/utils"
)

var (
	metricsAddressMap = make(map[string]string)
)

func init() {
	ip, err := utils.GetMetadata(utils.PrivateIpv4Item)
	if err != nil {
		ip, err = utils.GetMetadata(utils.Ipv6Item)
		if err != nil {
			return
		}
	}

	metricsAddressMap["alertmanager"] = fmt.Sprintf("%s:9093", ip)
	metricsAddressMap["prometheus"] = fmt.Sprintf("%s:9093", ip)
	metricsAddressMap["grafana"] = fmt.Sprintf("%s:9093", ip)
}

func ProxyMetrics(c *gin.Context) {
	log.Info("PROXY METRICS")
	url, newPath, err := getMetricsUrl(c.Request.URL.Path)
	if err != nil {
		log.WithError(err).Error("failed to get metrics url from path")
		c.Error(utils.UnknownApiErr(err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	c.Request.URL.Path = newPath

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			// r.URL.Host, r.URL.Path = url, path.Clean("/"+newPath)
			r.URL.Host = url
			r.URL.Scheme = "http"
			log.Info("metrics url: ", r.URL)
		},
		ModifyResponse: func(r *http.Response) error {
			b, _ := io.ReadAll(r.Body)
			log.Info("metrics resp body: ", string(b))

			return nil
		},
	}).ServeHTTP(c.Writer, c.Request)
}

func getMetricsUrl(path string) (string, string, error) {
	splittedPath := strings.Split(path, "/")
	if len(splittedPath) < 3 {
		return "", "", fmt.Errorf("metric tool for empty path is not found")
	}

	url, ok := metricsAddressMap[splittedPath[2]]
	if !ok {
		return "", "", fmt.Errorf("metric tool for path '%v' is not found", splittedPath[0])
	}

	newPath := ""
	if len(splittedPath) > 3 {
		newPath = strings.Join(splittedPath[3:], "/")
	}

	log.Infof("new path: %s, new url: %s", newPath, url)
	return url, newPath, nil
}
