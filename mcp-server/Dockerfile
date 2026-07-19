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
RUN CGO_ENABLED=0 go build -o /out/workspace-mcp ./cmd/workspace-mcp

FROM alpine:3.22

COPY --from=build /out/workspace-mcp /usr/local/bin/workspace-mcp

EXPOSE 8081

ENTRYPOINT ["/usr/local/bin/workspace-mcp"]
