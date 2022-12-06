
.PHONY: all imports fmt test

all: imports fmt test

imports:
	goimports -l -w $$(go list -f {{.Dir}} ./... | grep -v /vendor/)
fmt:
	gofmt -s -l -w $$(go list -f {{.Dir}} ./... | grep -v /vendor/)
test: 
	go test -race $$(go list ./... | grep -v /vendor/) -coverprofile cover.out


