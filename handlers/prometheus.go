package handlers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"

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

	metricsAddressMap["alertmanager"] = &metric{address: fmt.Sprintf("%s:9093", ip), path: "/alertmanager"}
	metricsAddressMap["prometheus"] = &metric{address: fmt.Sprintf("%s:9090", ip), path: "/prometheus"}
	metricsAddressMap["grafana"] = &metric{address: fmt.Sprintf("%s:3000", ip), path: "/grafana"}
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
			r.URL.Path = getRemainingPath(c.Request.URL.Path, 2)
		},
		ModifyResponse: func(r *http.Response) error {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			defer r.Body.Close()

			b = bytes.Replace(b,
				[]byte("href=\"/"),
				[]byte(fmt.Sprintf("href=\"%s/", metric.path)),
				-1)

			r.Body = io.NopCloser(bytes.NewReader(b))
			r.ContentLength = int64(len(b))
			r.Header.Set("Content-Length", strconv.Itoa(len(b)))

			locationValue := r.Header.Get("Location")
			if locationValue != "" {
				locationValue = metric.path + locationValue
				r.Header.Set("Location", locationValue)
			}

			return nil
		},
	}).ServeHTTP(c.Writer, c.Request)
}

func getMetric(path string) (*metric, error) {
	splittedPath := strings.Split(path, "/")
	if len(splittedPath) < 2 {
		return nil, fmt.Errorf("metric tool for empty path is not found")
	}

	metric, ok := metricsAddressMap[splittedPath[1]]
	if !ok {
		return nil, fmt.Errorf("metric for '%s' path is not found", splittedPath[1])
	}

	return metric, nil
}
