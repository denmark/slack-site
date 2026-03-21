# Build the slack-site binary. Rebuilds when any Go source file changes.
BINARY := slack-site
SOURCES := $(shell find . -name '*.go' -not -path './out/*' 2>/dev/null)
TEMPLATES := $(shell find . -name '*.html' -not -path './out/*' 2>/dev/null)

.PHONY: build clean test
.DEFAULT_GOAL := build

build: $(BINARY)

$(BINARY): $(SOURCES) $(TEMPLATES)
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)
