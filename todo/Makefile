BINARY       := todo
IMAGE        := localhost/todo:latest
CONTAINER_ENGINE := podman
QUADLET_DIR  := $(HOME)/.config/containers/systemd

QUADLET_FILES := $(wildcard deploy/*.container deploy/*.volume deploy/*.network)
SERVICES     := todo-mcp postgres chromadb

.PHONY: all build lint test clean image push deploy

all: lint test build

build:
	go build -o $(BINARY) .

lint:
	golangci-lint run ./...

test:
	go test ./... -count=1

clean:
	rm -f $(BINARY)

image:
	$(CONTAINER_ENGINE) build -t $(IMAGE) .

push:
	$(CONTAINER_ENGINE) push $(IMAGE)

deploy: image push
	mkdir -p $(QUADLET_DIR)
	cp $(QUADLET_FILES) $(QUADLET_DIR)/
	systemctl --user daemon-reload
	systemctl --user restart $(SERVICES)
