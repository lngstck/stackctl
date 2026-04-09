# stackctl – build, dev, deploy
#
# Targets gemäß ARCHITECTURE.md §14 (Build) und §15 (Entwicklungs-Workflow).

VERSION            := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS            := -s -w -X main.version=$(VERSION)
PKG                := ./cmd/stackctl
DIST               := dist
LEARNINGSTACK_DEVBOX ?= learningstack-local

.PHONY: all build build-linux-amd64 build-linux-arm64 build-all dev run \
        test vet fmt clean deploy-devbox version

all: build

## build: native Build für die aktuelle Plattform (Entwicklung)
build:
	@mkdir -p $(DIST)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/stackctl $(PKG)

## build-linux-amd64: Cross-Compile für x86_64-Server
build-linux-amd64:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/stackctl-linux-amd64 $(PKG)

## build-linux-arm64: Cross-Compile für arm64-Server
build-linux-arm64:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/stackctl-linux-arm64 $(PKG)

## build-all: alle Release-Targets
build-all: build-linux-amd64 build-linux-arm64

## dev: Web-UI lokal im Dev-Modus starten
dev:
	go run $(PKG) web --dev --host 127.0.0.1 --port 8090

## run: Web-UI lokal starten (ohne --dev)
run:
	go run $(PKG) web --host 127.0.0.1 --port 8090

## test: alle Tests
test:
	go test ./...

## vet: go vet
vet:
	go vet ./...

## fmt: gofmt -s -w .
fmt:
	gofmt -s -w .

## clean: dist/ löschen
clean:
	rm -rf $(DIST)

## deploy-devbox: für Linux/amd64 bauen und auf Devbox deployen
deploy-devbox:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/stackctl-dev $(PKG)
	rsync -avz $(DIST)/stackctl-dev $(LEARNINGSTACK_DEVBOX):/tmp/stackctl
	ssh $(LEARNINGSTACK_DEVBOX) 'sudo mv /tmp/stackctl /opt/stackctl/stackctl && sudo systemctl restart stackctl'

## version: aktuelle Versionskennung ausgeben
version:
	@echo $(VERSION)
