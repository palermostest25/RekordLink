VERSION ?= 0.4.5
GOCACHE ?= /tmp/rekordlink-gocache

.PHONY: build test vet package docker-build clean

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/rekordlink ./cmd/rekordlink

test:
	GOCACHE=$(GOCACHE) go test ./...

vet:
	GOCACHE=$(GOCACHE) go vet ./...

package: test vet
	GOCACHE=$(GOCACHE) ./scripts/package.sh $(VERSION)

docker-build:
	docker build --build-arg VERSION=$(VERSION) --tag rekordlink-relay:$(VERSION) .

clean:
	rm -rf bin dist
