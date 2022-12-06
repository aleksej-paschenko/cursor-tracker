
.PHONY: all imports fmt test

all: imports fmt test

imports:
	goimports -l -w $$(go list -f {{.Dir}} ./... | grep -v /vendor/)
fmt:
	gofmt -s -l -w $$(go list -f {{.Dir}} ./... | grep -v /vendor/)
test: 
	go test -race $$(go list ./... | grep -v /vendor/) -coverprofile cover.out
run:
	go run cmd/main.go
lint-install:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1

lint:
	golangci-lint