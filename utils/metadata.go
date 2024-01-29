package utils

import (
	"fmt"
	"io/ioutil"
	"net/http"
)

type metadataItem string

var (
	InstanceIdItem metadataItem = "instance-id"
)

const (
	tokenURL    = "http://169.254.169.254/latest/api/token"
	metadataURL = "http://169.254.169.254/latest/meta-data"
)

func GetMetadata(item metadataItem) (string, error) {
	tokenBytes, err := getToken("10")
	if err != nil {
		return "", err
	}

	generateMetadataReq, err := http.NewRequest("GET", fmt.Sprintf("%s/%s", metadataURL, item), nil)
	if err != nil {
		return "", err
	}

	generateMetadataReq.Header.Set("X-aws-ec2-metadata-token", string(tokenBytes))
	response, err := http.DefaultClient.Do(generateMetadataReq)
	if err != nil {
		return "", err
	}

	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	return string(body), err
}

func RefreshIMDSV2Token() error {
	tokenBytes, err := getToken("21600")
	if err != nil {
		return err
	}

	generateMetadataReq, err := http.NewRequest("GET", metadataURL, nil)
	if err != nil {
		return err
	}

	generateMetadataReq.Header.Set("X-aws-ec2-metadata-token", string(tokenBytes))
	_, err = http.DefaultClient.Do(generateMetadataReq)

	return err
}

func getToken(ttlSeconds string) ([]byte, error) {
	generateTokenReq, err := http.NewRequest("PUT", tokenURL, nil)
	if err != nil {
		return nil, err
	}

	generateTokenReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", ttlSeconds)
	tokenResp, err := http.DefaultClient.Do(generateTokenReq)
	if err != nil {
		return nil, err
	}

	defer tokenResp.Body.Close()
	body, err := ioutil.ReadAll(tokenResp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}
