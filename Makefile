PROJECTNAME=$(shell basename "$(PWD)")
GOPATH := $(shell go env GOPATH)
GOLANGCI := $(GOPATH)/bin/golangci-lint
MOCKGEN := $(GOPATH)/bin/mockgen

.PHONY: help all build test lint lint-fix genmocks get_mockgen get_lint

all: help

help: Makefile
	@echo
	@echo "Choose a make command to run in "$(PROJECTNAME)":"
	@echo
	@$(MAKE) -pRrq -f $(lastword $(MAKEFILE_LIST)) : 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | sort | egrep -v -e '^[^[:alnum:]]' -e '^$@$$'
	@echo

build:
	go build ./...

test:
	go test -race ./...

get_mockgen:
	@if [ ! -f $(MOCKGEN) ]; then \
		go install go.uber.org/mock/mockgen@latest; \
	fi

genmocks: get_mockgen
	$(MOCKGEN) -source=./internal/chain/client.go -destination=./internal/chain/mocks/client.go -package=mocks

get_lint:
	@if [ ! -f $(GOLANGCI) ]; then \
		curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOPATH)/bin v1.60.0; \
	fi

lint: get_lint
	$(GOLANGCI) run

lint-fix: get_lint
	$(GOLANGCI) run --fix
