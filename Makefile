# Build the slack-site binary. Rebuilds when any Go source file changes.
BINARY := slack-site
SOURCES := $(shell find . -name '*.go' -not -path './out/*' 2>/dev/null)

.PHONY: build clean
.DEFAULT_GOAL := build

build: $(BINARY)

$(BINARY): $(SOURCES)
	go build -o $(BINARY) .

clean:
	rm -f $(BINARY)
