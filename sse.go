package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"net/http"
)

const tokenLength = 25

type Broker struct {
	Notifier         chan ContainerLogMessage
	incomingClients  chan *ClientInfo
	outcomingClients chan *ClientInfo
	clients          map[string]map[string]chan []byte
}

type StreamId struct {
	ContainerId string
	Host        string
}

type ClientInfo struct {
	ClientToken string
	StreamId    StreamId
	Channel     chan []byte
}

func newSSE() *Broker {
	broker := &Broker{
		Notifier:         make(chan ContainerLogMessage, 1),
		incomingClients:  make(chan *ClientInfo),
		outcomingClients: make(chan *ClientInfo),
		clients:          make(map[string]map[string]chan []byte),
	}
	go broker.listen()
	return broker
}

func StartSSE(streamer *chan ContainerLogMessage) {
	log.Info("Starting Server sent event")
	b := newSSE()
	go func() {
		for {
			b.Notifier <- <-*streamer
		}
	}()
	http.Handle("/logs", b)
	log.Fatal("HTTP server error: ", http.ListenAndServe("localhost:12345", nil))
}

func (b *Broker) listen() {
	for {
		select {
		case x := <-b.incomingClients:
			streamId := x.StreamId.Host + x.StreamId.ContainerId
			if _, exists := b.clients[streamId]; !exists {
				log.WithField("streamId", streamId).Debug("Creating multi-client map for container")
				b.clients[streamId] = make(map[string]chan []byte)
			}
			b.clients[streamId][x.ClientToken] = x.Channel
			log.WithField("clients size", len(b.clients)).Info("New client")
		case x := <-b.outcomingClients:
			streamId := x.StreamId.Host + x.StreamId.ContainerId
			close(x.Channel)
			delete(b.clients[streamId], x.ClientToken)
			if len(b.clients[streamId]) == 0 {
				log.WithField("streamId", streamId).Debug("No more clients listening to this container logs, deleting multi-client map entry")
				delete(b.clients, streamId)
			}
			log.WithField("clients size", len(b.clients)).Info("Delete client")
		case x := <-b.Notifier:
			streamId := x.StreamId.Host + x.StreamId.ContainerId
			if _, exists := b.clients[streamId]; exists {
				for clientToken, clientChannel := range b.clients[streamId] {
					log.WithField("clientToken", clientToken).Debug("Sending to client")
					clientChannel <- x.Message
				}
			}
		}
	}
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	host := req.URL.Query().Get("host")
	if host == "" {
		http.Error(w, "missing host parameter", http.StatusBadRequest)
	}
	containerId := req.URL.Query().Get("containerId")
	if containerId == "" {
		http.Error(w, "missing containerId parameter", http.StatusBadRequest)
	}

	containerLogChannel := &ClientInfo{
		ClientToken: generateToken(), // identify client through channel collections
		StreamId: StreamId{
			Host:        host,
			ContainerId: containerId,
		},
		Channel: make(chan []byte),
	}

	b.incomingClients <- containerLogChannel

	notify := w.(http.CloseNotifier).CloseNotify()

	go func() {
		<-notify
		b.outcomingClients <- containerLogChannel
	}()

	for {
		message, opened := <-containerLogChannel.Channel
		if !opened {
			break
		}
		fmt.Fprintf(w, "data: %s\n\n", message)
		flusher.Flush()
	}

}

func generateToken() string {
	return RandStringBytesMaskImprSrc(tokenLength)
}
