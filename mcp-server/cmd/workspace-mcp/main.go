package main

import (
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/patrhez/agent-platform/mcp-server/internal/workspace"
)

const defaultListenAddress = ":8081"

func main() {
	service, err := workspace.New(os.Getenv("WORKSPACE_ROOT"))
	if err != nil {
		log.Printf("configure workspace service: %v", err)
		os.Exit(1)
	}
	server := workspace.NewMCPServer(service)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	mux.HandleFunc("GET /healthz", func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.WriteHeader(http.StatusOK)
	})

	if err := http.ListenAndServe(defaultListenAddress, mux); err != nil {
		log.Printf("serve workspace MCP: %v", err)
		os.Exit(1)
	}
}
