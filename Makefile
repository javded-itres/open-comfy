MODULE := github.com/javded-itres/open-comfy
VERSION ?= 0.1.1
LDFLAGS := -s -w -X main.version=$(VERSION)
CGO := 0

.PHONY: build test vet tidy clean

build:
	CGO_ENABLED=$(CGO) go build -trimpath -ldflags "$(LDFLAGS)" -o opencomfy ./cmd/opencomfy

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -f opencomfy
