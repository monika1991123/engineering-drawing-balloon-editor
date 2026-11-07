BIN     := bin/balloon
PKG     := ./cmd/balloon
GOFILES := $(shell find . -name '*.go' -not -path './web/*')
WEBFILES := $(shell find web -type f -not -name '*.test.mjs')

.PHONY: all build test cover vet fmt demo serve clean setup ocr-test skill-test

all: fmt vet test build

build: $(BIN)

$(BIN): $(GOFILES) $(WEBFILES) go.mod go.sum
	go build -o $(BIN) $(PKG)

test:
	go test ./...

PYTHON ?= python3
SKILL  := skill/engineering-drawing-balloon

# One .venv serves both local OCR and the bundled skill (Python 3.11+).
setup:
	$(PYTHON) -m venv .venv
	.venv/bin/python -m pip install -r ocr/requirements.txt -r $(SKILL)/requirements.txt

ocr-test:
	.venv/bin/python -m unittest discover -s ocr -v

skill-test:
	.venv/bin/python $(SKILL)/tests/smoke_test.py
	.venv/bin/python $(SKILL)/tests/test_arrow_layout.py

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

fmt:
	gofmt -l -w ./cmd ./internal

# Regenerates the picture in the README.
demo: $(BIN)
	./$(BIN) demo -o docs/demo.svg

serve: $(BIN)
	./$(BIN) serve

clean:
	rm -rf bin coverage.out
