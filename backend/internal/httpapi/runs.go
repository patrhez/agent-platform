package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
)

const maxArtifactResponseBytes int64 = 10 << 20

func (server *Server) getRun(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	snapshot, err := server.store.GetRunSnapshot(ctx, principal.UserID(), request.PathValue("runID"))
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	writeJSON(ctx, server.logger, responseWriter, http.StatusOK, snapshot)
}

func (server *Server) getRunTrace(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	trace, err := server.store.GetRunTrace(ctx, principal.UserID(), request.PathValue("runID"))
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	writeJSON(ctx, server.logger, responseWriter, http.StatusOK, trace)
}

func (server *Server) getArtifact(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	artifact, err := server.store.GetArtifact(
		ctx, principal.UserID(), request.PathValue("runID"), request.PathValue("artifactID"),
	)
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	if artifact.ByteSize > maxArtifactResponseBytes {
		writeError(
			ctx, server.logger, responseWriter, http.StatusRequestEntityTooLarge,
			"artifact_too_large", fmt.Errorf("Artifact exceeds the download limit"),
		)
		return
	}
	responseWriter.Header().Set("Content-Type", artifact.ContentType)
	responseWriter.Header().Set("Content-Length", strconv.FormatInt(artifact.ByteSize, 10))
	responseWriter.Header().Set("Content-Disposition", "attachment; filename=\""+artifact.ID+"\"")
	if _, err := responseWriter.Write(artifact.Content); err != nil {
		return
	}
}

func (server *Server) cancelRun(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	snapshot, err := server.store.RequestRunCancellation(ctx, principal.UserID(), request.PathValue("runID"))
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	writeJSON(ctx, server.logger, responseWriter, http.StatusAccepted, snapshot)
}
