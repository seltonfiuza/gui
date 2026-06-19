BINARY := gui
PKG    := ./...
GOBIN  := $(shell go env GOPATH)/bin

.DEFAULT_GOAL := build

.PHONY: build run install test vet fmt tidy clean check all

## build: compile the gui binary into ./$(BINARY)
build:
	go build -o $(BINARY) .

## run: build and launch the TUI in the current repo
run:
	go run .

## install: install the gui binary into $(GOBIN)
install:
	go install .

## test: run all unit tests
test:
	go test $(PKG)

## vet: run go vet across all packages
vet:
	go vet $(PKG)

## fmt: format all Go source
fmt:
	go fmt $(PKG)

## tidy: sync go.mod / go.sum
tidy:
	go mod tidy

## check: fmt + vet + test (CI gate)
check: fmt vet test

## all: tidy, check, and build
all: tidy check build

## clean: remove build artifacts
clean:
	rm -f $(BINARY)
	go clean
