package service

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	"time"
)

type instanceWatchWorker struct {
	instances map[string]*ec2.Instance
}

func (w *instanceWatchWorker) start() {
	for {
		time.Sleep(5 * time.Second)

	}
}
