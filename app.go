package main

import (
	"context"
	pb "docker-visualizer/proto/containers"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/moby/moby/client"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"strings"
)

type dockerLog struct {
	Message        string `json:"message,omitempty"`
	IsErrorMessage bool   `json:"isErrorMessage"`
}

var httpClient *http.Client // client for Docker API calls TODO(ArchangelX360): add TLS option
var hostToCli map[string]*client.Client
var streamPipe chan ContainerLogMessage

func send(logMessage []byte, isErrorMessage bool, containerInfo *pb.ContainerInfo) {
	dockerLog := dockerLog{
		Message:        string(logMessage),
		IsErrorMessage: isErrorMessage,
	}
	byteEncodedDockerLog, err := json.Marshal(dockerLog)
	if err != nil {
		log.Fatal(err)
	}
	log.WithField("message", string(byteEncodedDockerLog)).Info("Sending message to channel")
	containerLogMessage := ContainerLogMessage{
		StreamId: StreamId{
			Host:        containerInfo.Host,
			ContainerId: containerInfo.Id,
		},
		Message: byteEncodedDockerLog,
	}
	streamPipe <- containerLogMessage
}

func createHostCliIfNotExists(info *pb.ContainerInfo) error {
	if _, cliAlreadyExists := hostToCli[info.Host]; !cliAlreadyExists {
		cli, err := client.NewClient(info.Host, client.DefaultVersion, httpClient, nil)
		if err != nil {
			return err
		}
		hostToCli[info.Host] = cli
	}
	return nil
}

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

func startContainerLogging(w http.ResponseWriter, r *http.Request) {
	containerInfo, err := parseBodyToContainerInfo(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Info("Requesting channel for logs of container: " + containerInfo.Id)

	if err := createHostCliIfNotExists(containerInfo); err != nil {
		log.Errorf("error creating Docker client: %v", err)
		return
	}

	// FIXME: stop this co routine somewhere
	go func(containerInfo *pb.ContainerInfo) {

		reader, err := hostToCli[containerInfo.Host].ContainerLogs(context.Background(), containerInfo.Id, types.ContainerLogsOptions{
			ShowStdout: true,
			Follow:     true,
		})
		if err != nil {
			log.Error(err)
			return
		}
		defer reader.Close()
		hdr := make([]byte, 8)
		for {
			_, err := reader.Read(hdr)
			if err != nil {
				log.Error(err)
				return
			}
			isErrorMessage := hdr[0] != 1
			count := binary.BigEndian.Uint32(hdr[4:])
			dat := make([]byte, count)
			_, err = reader.Read(dat)
			trimedDat := strings.TrimSuffix(string(dat), "\n")
			if isErrorMessage {
				log.Debug("[DOCKER ERROR] " + trimedDat)
			} else {
				log.Debug(trimedDat)
			}

			send(dat, isErrorMessage, containerInfo)
		}
	}(containerInfo)

	encoder := json.NewEncoder(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := encoder.Encode(containerInfo); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "{\"message\":\"error during containerInfo JSON parsing\"}")
	}
}

func closeDockerClients() {
	for _, dockerClient := range hostToCli {
		dockerClient.Close()
	}
}

func main() {
	defer closeDockerClients()

	log.SetLevel(log.DebugLevel)

	hostToCli = make(map[string]*client.Client)
	streamPipe = make(chan ContainerLogMessage)
	go StartSSE(&streamPipe)

	mux := http.NewServeMux()
	mux.HandleFunc("/", startContainerLogging)
	handler := cors.Default().Handler(mux)
	http.ListenAndServe(":8090", handler)
}
