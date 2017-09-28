# metrics-gatherer
Gathers metrics and logs of docker containers or hosts for the Falcon stack

## Open a channel

```
curl -X POST \
  http://localhost:8090/ \
  -H 'cache-control: no-cache' \
  -H 'content-type: application/json' \
  -H 'postman-token: ec64a44e-c07d-1a76-d6a8-30707ba9d914' \
  -d '{
	"id":"35513ea9b3f7",
	"name":"",
	"ip":"",
	"network":"",
	"service":"",
	"stack":"",
	"host":"unix:///var/run/docker.sock"
}'
```

## Listen to a channel

Open an SSE connection to:

`http://localhost:12345/logs?host=unix:///var/run/docker.sock&containerId=35513ea9b3f7`