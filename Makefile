REGISTRY ?= ghcr.io
REPO ?= qinqon/oompa
TAG ?= latest
IMAGE = $(REGISTRY)/$(REPO):$(TAG)
CONTAINER_CMD ?= podman

.PHONY: build test lint fmt check-fmt fix check-fix image push clean

build:
	go build -o oompa ./cmd/oompa

test:
	go test ./...

lint:
	go vet ./...
	golangci-lint run

fmt:
	gofmt -w .

check-fmt:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted files:"; echo "$$unformatted"; \
		echo "run 'make fmt' and commit"; exit 1; \
	fi

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
