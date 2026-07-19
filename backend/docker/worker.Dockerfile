FROM golang:1.25-alpine AS build

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ARG NO_PROXY
ENV HTTP_PROXY=${HTTP_PROXY} HTTPS_PROXY=${HTTPS_PROXY} ALL_PROXY=${ALL_PROXY} NO_PROXY=${NO_PROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM alpine:3.22

COPY --from=build /out/worker /usr/local/bin/worker
COPY configs/agents/issue-troubleshooter.yaml /etc/agent-platform/issue-troubleshooter.yaml

ENTRYPOINT ["/usr/local/bin/worker"]
