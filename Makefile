# stackctl – build, dev, deploy
#
# Targets gemäß ARCHITECTURE.md §14 (Build) und §15 (Entwicklungs-Workflow).

VERSION            := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS            := -s -w -X main.version=$(VERSION)
PKG                := ./cmd/stackctl
DIST               := dist
LEARNINGSTACK_DEVBOX ?= learningstack-local

.PHONY: all build build-linux-amd64 build-linux-arm64 build-all checksums dev run \
        test vet fmt clean deploy-devbox release release-manual version

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

## build-all: alle Release-Targets + SHA256SUMS
build-all: build-linux-amd64 build-linux-arm64 checksums

## checksums: SHA256SUMS über die Release-Binaries erzeugen (von Self-Update
## verifiziert, s. internal/update). Dateinamen ohne dist/-Prefix, damit die
## Einträge zu den Asset-Namen im Release passen.
checksums:
	cd $(DIST) && shasum -a 256 stackctl-linux-amd64 stackctl-linux-arm64 > SHA256SUMS
	@echo "✓ $(DIST)/SHA256SUMS"

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

## release: Tag setzen und pushen — das Bauen + Veröffentlichen übernimmt der
## GitHub-Actions-Workflow (.github/workflows/release.yml). Weil CI den Tag
## auscheckt, trägt `git describe` dort exakt die Tag-Version; die frühere
## "Tag vor Build"-Fragilität entfällt.
## Nutzung: make release TAG=v0.1.0
release:
	@test -n "$(TAG)" || (echo "Nutzung: make release TAG=v0.1.0" && exit 1)
	@git diff --quiet || (echo "Working tree dirty — commit/stash erst." && exit 1)
	git tag -a $(TAG) -m "Release $(TAG)"
	git push origin $(TAG)
	@echo "✓ Tag $(TAG) gepusht — GitHub Actions baut und veröffentlicht das Release."

## release-manual: Fallback, falls CI nicht verfügbar ist. Erwartet, dass der
## Tag bereits existiert/ausgecheckt ist (sonst stimmt die Binär-Version nicht),
## baut lokal und lädt die Assets ins bestehende Release hoch.
## Nutzung: make release-manual TAG=v0.1.0
release-manual: build-all
	@test -n "$(TAG)" || (echo "Nutzung: make release-manual TAG=v0.1.0" && exit 1)
	gh release create $(TAG) \
		$(DIST)/stackctl-linux-amd64 \
		$(DIST)/stackctl-linux-arm64 \
		$(DIST)/SHA256SUMS \
		--title "$(TAG)" \
		--generate-notes

## version: aktuelle Versionskennung ausgeben
version:
	@echo $(VERSION)
