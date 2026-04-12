BINARY       := todo
IMAGE        := localhost/todo:latest
CONTAINER_ENGINE := podman
QUADLET_DIR  := $(HOME)/.config/containers/systemd

QUADLET_FILES := $(wildcard deploy/*.container deploy/*.volume deploy/*.network)
SERVICES     := todo-mcp postgres chromadb

.PHONY: all build lint test clean image push deploy undeploy reset

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

deploy: image
	mkdir -p $(QUADLET_DIR)
	cp $(QUADLET_FILES) $(QUADLET_DIR)/
	systemctl --user daemon-reload
	systemctl --user restart $(SERVICES)

undeploy:
	systemctl --user stop $(SERVICES) || true
	cd $(QUADLET_DIR) && rm -f $(notdir $(QUADLET_FILES))
	systemctl --user daemon-reload

reset:
	systemctl --user stop $(SERVICES) || true
	$(CONTAINER_ENGINE) volume rm todo-pgdata todo-chromadata || true
	systemctl --user start $(SERVICES)
