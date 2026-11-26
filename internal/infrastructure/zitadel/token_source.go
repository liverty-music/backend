package zitadel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// tokenRequestTimeout bounds one token request.
const tokenRequestTimeout = 30 * time.Second

// serverErrorTransport fails a request the server answers with a server error.
// The token source reports such an answer as a plain error that cannot be told
// from a refused request; a failed round trip reaches the caller as a
// *url.Error, as a request that could not be sent does.
type serverErrorTransport struct{}

// RoundTrip implements http.RoundTripper.
func (serverErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("token endpoint answered %s", resp.Status)
	}
	return resp, nil
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
			profile.WithStaticTokenEndpoint(issuer, tokenEndpoint(issuer)),
			profile.WithHTTPClient(&http.Client{Timeout: tokenRequestTimeout, Transport: serverErrorTransport{}}))
	}
}

// tokenUnreachable reports whether a call failed because no sign-in token
// could be fetched for it: the request could not be sent, or the token
// endpoint answered a server error (serverErrorTransport). Such a failure
// carries no gRPC status.
func tokenUnreachable(err error) bool {
	_, ok := errors.AsType[*url.Error](err)
	return ok
}
