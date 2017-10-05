BINARY = metrics-gatherer
DIR = metrics-gatherer
GOARCH = amd64

VERSION=1.0
COMMIT=$(shell git rev-parse HEAD)
BRANCH=$(shell git rev-parse --abbrev-ref HEAD)

BUILD_DIR=${GOPATH}/src/docker-visualizer/${DIR}/dist

LDFLAGS = -ldflags "-X main.VERSION=${VERSION} -X main.COMMIT=${COMMIT} -X main.BRANCH=${BRANCH} -linkmode external -extldflags -static"


all: clean linux

linux:
	mkdir -p ${BUILD_DIR}; \
	cd ${BUILD_DIR}; \
	GOOS=linux GOARCH=${GOARCH} go build ${LDFLAGS} -o ${BINARY}-linux-${GOARCH} ../ ; \
	cd .. >/dev/null

clean:
	-rm -f ${BINARY}-*


.PHONY: link linux clean