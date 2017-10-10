package main

import (
	"context"
	pb "docker-visualizer/proto/containers"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"net/http"
	"strings"
	"sync"
	"time"
)

const logChannelTimeout = 15 * time.Minute

type dockerLog struct {
	Message        string `json:"message,omitempty"`
	IsErrorMessage bool   `json:"isErrorMessage"`
}

type ContainerLogMessage struct {
	StreamId StreamId
	Message  []byte // an encoded docker log
}

type LogStreamer struct {
	StreamPipe    chan ContainerLogMessage
	HostToCli     map[string]*client.Client
	OpenedStreams map[StreamId]bool
	MapLock       *sync.WaitGroup
	StreamsLock   *sync.WaitGroup
	RootCtx       context.Context
	RootCancel    context.CancelFunc
}

func (ls *LogStreamer) startContainerLogging(w http.ResponseWriter, r *http.Request) {
	containerInfo, err := parseBodyToContainerInfo(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := ls.createHostCliIfNotExists(containerInfo); err != nil {
		log.Errorf("error creating Docker client: %v", err)
		return
	}

	ls.MapLock.Wait()
	streamId := &StreamId{Host: containerInfo.Host, ContainerId: containerInfo.Id}
	log.WithField("streamId", streamId).Info("Requesting channel for logs")

	if _, alreadyOpened := ls.OpenedStreams[*streamId]; !alreadyOpened {
		ls.MapLock.Add(1)
		ls.StreamsLock.Add(1)
		go ls.openLogStream(ls.MapLock, ls.RootCtx, streamId)
	} else {
		log.WithField("streamId", streamId).Info("log stream already opened")
	}

	encoder := json.NewEncoder(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := encoder.Encode(containerInfo); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "{\"message\":\"error during containerInfo JSON parsing\"}")
	}
}

func (ls *LogStreamer) send(dockerLog *dockerLog, streamId *StreamId) {
	byteEncodedDockerLog, err := json.Marshal(dockerLog)
	if err != nil {
		log.Fatal(err)
	}
	log.WithField("message", string(byteEncodedDockerLog)).Debug("Sending message to channel")
	containerLogMessage := ContainerLogMessage{
		StreamId: *streamId,
		Message:  byteEncodedDockerLog,
	}
	ls.StreamPipe <- containerLogMessage
}

func (ls *LogStreamer) createHostCliIfNotExists(info *pb.ContainerInfo) error {
	if _, cliAlreadyExists := ls.HostToCli[info.Host]; !cliAlreadyExists {
		cli, err := client.NewClient(info.Host, client.DefaultVersion, nil, nil)
		if err != nil {
			return err
		}
		ls.HostToCli[info.Host] = cli
	}
	return nil
}

func (ls *LogStreamer) openLogStream(mapLock *sync.WaitGroup, parent context.Context, streamId *StreamId) {
	defer ls.StreamsLock.Done()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	reader, err := ls.HostToCli[streamId.Host].ContainerLogs(ctx, streamId.ContainerId, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		log.Error(err)
		mapLock.Done()
		return
	}
	defer reader.Close()

	ls.OpenedStreams[*streamId] = true
	mapLock.Done()

	hdr := make([]byte, 8)

	logChan := make(chan *dockerLog)
	defer close(logChan)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
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
				logChan <- &dockerLog{
					Message:        string(dat),
					IsErrorMessage: isErrorMessage,
				}
			}
		}
	}()

	c := time.NewTicker(logChannelTimeout)
	defer c.Stop()
	for {
		select {
		case <-parent.Done():
			// this might be overkill
			delete(ls.OpenedStreams, *streamId)
			log.WithField("streamId", streamId).Info("Closing channel, program killed")
			return
		case <-c.C:
			// closing stream when timeout exceeded
			delete(ls.OpenedStreams, *streamId)
			log.WithField("streamId", streamId).Info("Log channel timed out")
			ls.send(&dockerLog{
				Message:        "Log channel timed out",
				IsErrorMessage: true,
			}, streamId)
			return
		case dockerLog := <-logChan:
			ls.send(dockerLog, streamId)
		}
	}
}

func (ls *LogStreamer) close() {
	for _, dockerClient := range ls.HostToCli {
		// closing docker clients
		dockerClient.Close()
	}
	// this might be overkill
	ls.RootCancel()
	ls.StreamsLock.Wait()
}
