package main

import (
	"context"
	"github.com/docker/docker/client"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
	"net/http"
	"sync"
)

func main() {
	log.SetLevel(log.DebugLevel)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	ls := &LogStreamer{
		StreamsLock:   &sync.WaitGroup{},
		MapLock:       &sync.WaitGroup{},
		HostToCli:     make(map[string]*client.Client),
		StreamPipe:    make(chan ContainerLogMessage),
		RootCtx:       rootCtx,
		RootCancel:    rootCancel,
		OpenedStreams: make(map[StreamId]bool),
	}
	defer ls.close()

	go StartSSE(&ls.StreamPipe)

	mux := http.NewServeMux()
	mux.HandleFunc("/", ls.startContainerLogging)
	handler := cors.Default().Handler(mux)
	http.ListenAndServe(":8090", handler)
}
