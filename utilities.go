package main

import (
	pb "docker-visualizer/proto/containers"
	"encoding/json"
	"errors"
	"io"
)

func parseBodyToContainerInfo(bodyStream io.ReadCloser) (*pb.ContainerInfo, error) {
	decoder := json.NewDecoder(bodyStream)
	var containerInfo pb.ContainerInfo
	err := decoder.Decode(&containerInfo)
	if err != nil {
		return nil, errors.New("can't parse body")
	}
	defer bodyStream.Close()

	return &containerInfo, nil
}
