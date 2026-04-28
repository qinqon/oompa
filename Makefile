REGISTRY ?= ghcr.io
REPO ?= qinqon/oompa
TAG ?= latest
IMAGE = $(REGISTRY)/$(REPO):$(TAG)
CONTAINER_CMD ?= podman

.PHONY: build test lint fix check-fix image push clean

build:
	go build -o oompa ./cmd/oompa

test:
	go test ./...

lint:
	go vet ./...

fix:
	go fix ./...

check-fix:
	@go fix ./... && git diff --exit-code || (echo "go fix ./... produced changes; run 'make fix' and commit" && exit 1)

image:
	$(CONTAINER_CMD) build -t $(IMAGE) -f Containerfile .

push: image
	$(CONTAINER_CMD) push $(IMAGE)

clean:
	rm -f oompa
