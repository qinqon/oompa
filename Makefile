REGISTRY ?= ghcr.io
REPO ?= qinqon/oompa
TAG ?= latest
IMAGE = $(REGISTRY)/$(REPO):$(TAG)
CONTAINER_CMD ?= podman

.PHONY: build test lint image push clean

build:
	go build -o oompa ./cmd/oompa

test:
	go test ./...

lint:
	go vet ./...

image:
	$(CONTAINER_CMD) build -t $(IMAGE) -f Containerfile .

push: image
	$(CONTAINER_CMD) push $(IMAGE)

clean:
	rm -f oompa
