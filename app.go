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
	"metrics-gatherer/sse"
	"net/http"
)

type dockerLog struct {
	Message        []byte `json:"message,omitempty"`
	IsErrorMessage bool   `json:"isErrorMessage"`
}

var httpClient *http.Client // client for Docker API calls TODO(ArchangelX360): add TLS option
var hostToCli map[string]*client.Client
var containerIdToChan map[string]*chan sse.ContainerLogMessage

func send(streamPipe *chan sse.ContainerLogMessage, logMessage []byte, isErrorMessage bool, containerInfo *pb.ContainerInfo) {
	dockerLog := dockerLog{
		Message:        logMessage,
		IsErrorMessage: isErrorMessage,
	}
	d, err := json.Marshal(dockerLog)
	if err != nil {
		log.Fatal(err)
	}
	containerLogMessage := sse.ContainerLogMessage{
		StreamId: sse.StreamId{
			Host:        containerInfo.Host,
			ContainerId: containerInfo.Id,
		},
		Message: d,
	}
	*streamPipe <- containerLogMessage
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

func createChanIfNotExists(info *pb.ContainerInfo) string {
	streamId := info.Host + "-" + info.Id
	if _, streamAlreadyExists := containerIdToChan[info.Id]; !streamAlreadyExists {
		streamPipe := make(chan sse.ContainerLogMessage)

		go sse.Start(&streamPipe)
		containerIdToChan[info.Id] = &streamPipe

		log.Infoln("Channel created for container: " + info.Id)
	} else {
		log.Infoln("Channel already existing for container: " + info.Id)
		log.Infoln("You can listen to it at: /logs/" + info.Id)
	}
	return streamId
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

func handler(w http.ResponseWriter, r *http.Request) {
	containerInfo, err := parseBodyToContainerInfo(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Infoln("Requesting channel for logs of container: " + containerInfo.Id)

	if err := createHostCliIfNotExists(containerInfo); err != nil {
		log.Errorf("error creating Docker client: %v", err)
		return
	}
	streamId := createChanIfNotExists(containerInfo)

	// FIXME: stop this co routine somewhere
	go func(containerInfo *pb.ContainerInfo) {

		reader, err := hostToCli[containerInfo.Host].ContainerLogs(context.Background(), containerInfo.Id, types.ContainerLogsOptions{
			ShowStdout: true,
			Follow:     true,
		})
		if err != nil {
			log.Errorln(err)
			return
		}
		defer reader.Close()
		hdr := make([]byte, 8)
		for {
			_, err := reader.Read(hdr)
			if err != nil {
				log.Errorln(err)
				return
			}
			isErrorMessage := hdr[0] != 1
			count := binary.BigEndian.Uint32(hdr[4:])
			dat := make([]byte, count)
			_, err = reader.Read(dat)
			if isErrorMessage {
				log.Errorln(string(dat))
			} else {
				log.Infoln(string(dat))
			}

			send(containerIdToChan[containerInfo.Id], dat, isErrorMessage, containerInfo)
		}
	}(containerInfo)

	streamIdJson := "{\"streamId\": \"" + streamId + "\"}"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprintln(w, streamIdJson)
}

func closeDockerClients() {
	for _, dockerClient := range hostToCli {
		dockerClient.Close()
	}
}

func closeSSEChannels() {
	for _, sseChannel := range containerIdToChan {
		close(*sseChannel)
	}
}

func main() {
	containerIdToChan = make(map[string]*chan sse.ContainerLogMessage)
	hostToCli = make(map[string]*client.Client)
	defer closeDockerClients()
	defer closeSSEChannels()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)
	handler := cors.Default().Handler(mux)
	http.ListenAndServe(":8090", handler)
}
