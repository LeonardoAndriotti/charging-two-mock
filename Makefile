.PHONY: run build test check fmt
run:
	go run ./cmd/api
build:
	go build ./...
test:
	go test -race ./...
fmt:
	gofmt -w cmd internal
check: test build
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)"
