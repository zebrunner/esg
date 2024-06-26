package prometheus

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/zebrunner/esg/environment/network"
)

var (
	exportersPorts = make(map[string]string, 0)
	mutex          = &sync.RWMutex{}
)

type Label struct {
	Job string
}

type Entity struct {
	Targets []string
	Label   Label
}

func SetExporter(network *network.NetworkConfiguration) error {
	url, ok := network.GetUrl("cadvisor")
	if !ok {
		return fmt.Errorf("failed to get cadvisor url")
	}

	mutex.RLock()
	file, _ := os.OpenFile("/etc/prometheus/prometheus-targets.yml", os.O_CREATE, os.ModePerm)
	defer file.Close()
	defer mutex.Unlock()

	exportersPorts[url.Port()] = url.Port()

	encoder := json.NewEncoder(file)
	data := []Entity{}
	for _, v := range exportersPorts {
		if _, err := strconv.Atoi(v); err == nil {
			data = append(data, Entity{
				[]string{fmt.Sprintf("%s:%s", "localhost", v)},
				Label{
					Job: "cadvisor",
				},
			})
		}
	}
	encoder.Encode(data)
	return nil
}

func DeleteExporter(network *network.NetworkConfiguration) error {
	url, ok := network.GetUrl("cadvisor")
	if !ok {
		return fmt.Errorf("failed to get cadvisor url")
	}

	mutex.RLock()
	file, _ := os.OpenFile("/etc/prometheus/prometheus-targets.yml", os.O_CREATE, os.ModePerm)
	defer file.Close()
	defer mutex.Unlock()

	delete(exportersPorts, url.Port())

	encoder := json.NewEncoder(file)
	data := []Entity{}
	for _, v := range exportersPorts {
		if _, err := strconv.Atoi(v); err == nil {
			data = append(data, Entity{
				[]string{fmt.Sprintf("%s:%s", "localhost", v)},
				Label{
					Job: "cadvisor",
				},
			})
		}
	}
	encoder.Encode(data)
	return nil
}
