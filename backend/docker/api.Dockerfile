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
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM alpine:3.22

COPY --from=build /out/api /usr/local/bin/api
COPY configs/agents/issue-troubleshooter.yaml /etc/agent-platform/issue-troubleshooter.yaml

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
