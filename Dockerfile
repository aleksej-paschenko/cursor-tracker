FROM golang:1.19-alpine AS builder

COPY go.mod /go/src/github.com/aleksej-paschenko/cursor-tracker/
COPY go.sum /go/src/github.com/aleksej-paschenko/cursor-tracker/
WORKDIR /go/src/github.com/aleksej-paschenko/cursor-tracker/

RUN go mod download 

COPY . /go/src/github.com/aleksej-paschenko/cursor-tracker/
WORKDIR /go/src/github.com/aleksej-paschenko/cursor-tracker/cmd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o tracker .

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder go/src/github.com/aleksej-paschenko/cursor-tracker/cmd/tracker /

USER nonroot:nonroot

ENTRYPOINT ["/tracker"]