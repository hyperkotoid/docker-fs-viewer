BINARY := bin/docker-fs-viewer
GOFLAGS := -ldflags=-linkmode=external

.PHONY: build test integration clean

build:
	mkdir -p bin
	go build $(GOFLAGS) -o $(BINARY) ./cmd/docker-fs-viewer

test:
	go test $(GOFLAGS) ./...

integration:
	go test $(GOFLAGS) -tags=integration ./integration -v

clean:
	rm -rf bin
