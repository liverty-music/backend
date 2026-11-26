package zitadel

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/client/profile"
	"github.com/zitadel/zitadel-go/v3/pkg/client"
	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	"golang.org/x/oauth2"
)

// tokenEndpoint returns Zitadel's OAuth token endpoint for issuer. It is the
// path Zitadel documents and the prod discovery document advertises, so the
// token source does not need discovery.
func tokenEndpoint(issuer string) string {
	return strings.TrimSuffix(issuer, "/") + "/oauth/v2/token"
}

// jwtProfileFromPath is middleware.JWTProfileFromPath with a static token
// endpoint. The library default discovers the endpoint over HTTP while the
// client is built, so a Zitadel outage at startup would stop the process.
// With a static endpoint, building the client makes no network request: the
// first token is fetched on the first call, and a failed fetch fails only that
// call and is retried by the next.
func jwtProfileFromPath(ctx context.Context, keyPath string) middleware.JWTProfileTokenSource {
	return func(issuer string, scopes []string) (oauth2.TokenSource, error) {
		key, err := client.ConfigFromKeyFile(keyPath)
		if err != nil {
			return nil, err
		}
		return profile.NewJWTProfileTokenSource(ctx, issuer, key.UserID, key.KeyID, key.Key, scopes,
			profile.WithStaticTokenEndpoint(issuer, tokenEndpoint(issuer)))
	}
}

// tokenUnreachable reports whether a call failed because no sign-in token
// could be fetched for it: the request could not be sent, or the token
// endpoint answered a server error. Such a failure carries no gRPC status.
func tokenUnreachable(err error) bool {
	if _, ok := errors.AsType[*url.Error](err); ok {
		return true
	}
	tokenErr, ok := errors.AsType[*oauth2.RetrieveError](err)
	return ok && tokenErr.Response != nil && tokenErr.Response.StatusCode >= http.StatusInternalServerError
}
