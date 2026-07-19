package httpapi

import (
	"context"
	"fmt"
	"net/http"
)

const (
	demoUserID      = "demo-user"
	demoTeamID      = "demo-team"
	demoDisplayName = "Demo User"
)

type principalContextKey struct{}

// Principal identifies the caller independently from the MVP's demo authentication.
type Principal interface {
	UserID() string
	TeamID() string
	DisplayName() string
}

type demoPrincipal struct{}

func (demoPrincipal) UserID() string      { return demoUserID }
func (demoPrincipal) TeamID() string      { return demoTeamID }
func (demoPrincipal) DisplayName() string { return demoDisplayName }

func (server *Server) withDemoPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		principal := demoPrincipal{}
		if err := server.store.EnsureUser(request.Context(), principal.UserID(), principal.TeamID(), principal.DisplayName()); err != nil {
			writeError(
				request.Context(), server.logger, responseWriter,
				http.StatusInternalServerError, "ensure_demo_principal", err,
			)
			return
		}
		ctx := context.WithValue(request.Context(), principalContextKey{}, Principal(principal))
		next.ServeHTTP(responseWriter, request.WithContext(ctx))
	})
}

func requestPrincipal(ctx context.Context) (Principal, error) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok {
		return nil, fmt.Errorf("request principal is missing")
	}
	return principal, nil
}
