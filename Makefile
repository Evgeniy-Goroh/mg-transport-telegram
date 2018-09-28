ROOT_DIR=$(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
SRC_DIR=$(ROOT_DIR)/src
MIGRATIONS_DIR=$(ROOT_DIR)/migrations
CONFIG_FILE=$(ROOT_DIR)/config.yml
CONFIG_TEST_FILE=$(ROOT_DIR)/config_test.yml
BIN=$(ROOT_DIR)/bin/transport
REVISION=$(shell git describe --tags 2>/dev/null || git log --format="v0.0-%h" -n 1 || echo "v0.0-unknown")

build: deps fmt
	@echo "==> Building"
	@cd $(SRC_DIR) && CGO_ENABLED=0 go build -o $(BIN) -ldflags "-X common.build=${REVISION}" .
	@echo $(BIN)

run: migrate
	@echo "==> Running"
	@${BIN} --config $(CONFIG_FILE) run

test: deps fmt
	@echo "==> Running tests"
	@cd $(ROOT_DIR) && go test ./... -v -cpu 2

jenkins_test: migrate_test
	@echo "==> Running tests (result in test-report.xml)"
	@go get -v -u github.com/jstemmer/go-junit-report
	@go test ./... -v -cpu 2 -race | /go/bin/go-junit-report -set-exit-code > ./test-report.xml
	@go mod tidy

fmt:
	@echo "==> Running gofmt"
	@gofmt -l -s -w $(ROOT_DIR)

deps:
	@echo "==> Installing dependencies"
	@go mod tidy

migrate: build
	${BIN} --config $(CONFIG_FILE) migrate -p $(MIGRATIONS_DIR)

migrate_test: build
	@${BIN} --config $(CONFIG_TEST_FILE) migrate -p $(MIGRATIONS_DIR)

migrate_down: build
	@${BIN} --config $(CONFIG_FILE) migrate -v down
