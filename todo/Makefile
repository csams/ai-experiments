BINARY       := todo
IMAGE        := localhost/todo:latest
CONTAINER_ENGINE := podman
QUADLET_DIR  := $(HOME)/.config/containers/systemd

CERT_DIR     := $(HOME)/.config/todo/certs
QUADLET_FILES := $(wildcard deploy/*.container deploy/*.volume deploy/*.network)
SERVICES     := todo-mcp postgres

.PHONY: all build lint test clean image push certs certs-renew deploy undeploy reset

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

certs:
	./scripts/generate-certs.sh $(CERT_DIR)

certs-renew:
	./scripts/generate-certs.sh --force $(CERT_DIR)

deploy: image certs
	mkdir -p $(QUADLET_DIR)
	mkdir -p $(HOME)/.config/todo/scripts
	cp scripts/pg-start-ssl.sh $(HOME)/.config/todo/scripts/
	cp deploy/production.yaml $(HOME)/.config/todo/production.yaml
	cp $(QUADLET_FILES) $(QUADLET_DIR)/
	systemctl --user daemon-reload
	systemctl --user restart $(SERVICES)

undeploy:
	systemctl --user stop $(SERVICES) || true
	cd $(QUADLET_DIR) && rm -f $(notdir $(QUADLET_FILES))
	systemctl --user daemon-reload

reset:
	systemctl --user stop $(SERVICES) || true
	$(CONTAINER_ENGINE) volume rm todo-pgdata || true
	systemctl --user start $(SERVICES)
