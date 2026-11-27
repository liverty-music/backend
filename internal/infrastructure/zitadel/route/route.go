// Package route sends the backend's calls to the sign-in service (ZITADEL)
// over the cluster network instead of the public load balancer.
//
// ZITADEL serves one instance under its public host. A call that reaches it
// at an internal address names that instance with the headers
// x-zitadel-instance-host and x-zitadel-public-host, as the hosted login app
// and the gateway already do. Everything that names the sign-in service to
// others (a token's `iss`, the audience of a JWT assertion, links in emails)
// keeps the public issuer URL; only where the bytes go changes.
package route

import (
	"fmt"
	"net/http"
	"net/url"

	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
)

// Headers that name the ZITADEL instance on a call to an internal address.
const (
	InstanceHostHeader = "x-zitadel-instance-host"
	PublicHostHeader   = "x-zitadel-public-host"
)

// Route says where a call meant for the sign-in service is sent.
type Route struct {
	issuerURL string
	issuer    *url.URL
	internal  *url.URL
}

// New returns the route to the sign-in service with the public issuer URL
// issuerURL. internalURL is its cluster-internal base URL
// (http://zitadel-api.zitadel.svc.cluster.local:80); when it is empty, calls
// go to the issuer itself, which only local runs do.
func New(issuerURL, internalURL string) (Route, error) {
	issuer, err := parseBase("issuer", issuerURL)
	if err != nil {
		return Route{}, err
	}
	r := Route{issuerURL: issuerURL, issuer: issuer}
	if internalURL != "" {
		if r.internal, err = parseBase("internal sign-in service", internalURL); err != nil {
			return Route{}, err
		}
	}
	return r, nil
}

// parseBase parses an absolute http or https URL with a host.
func parseBase(what, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s URL %q: %w", what, raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid %s URL %q: want an absolute http or https URL", what, raw)
	}
	return u, nil
}

// Issuer returns the public issuer URL as configured.
func (r Route) Issuer() string {
	return r.issuerURL
}

// Internal reports whether calls go to an internal address.
func (r Route) Internal() bool {
	return r.internal != nil
}

// Transport returns a round tripper that sends every request for the
// issuer's host to the internal address with both instance headers, and
// every other request unchanged, through next.
func (r Route) Transport(next http.RoundTripper) http.RoundTripper {
	if r.internal == nil {
		return next
	}
	return &transport{publicHost: r.issuer.Host, internal: r.internal, next: next}
}

// transport is the round tripper Route.Transport returns.
type transport struct {
	publicHost string
	internal   *url.URL
	next       http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.publicHost {
		return t.next.RoundTrip(req)
	}
	out := req.Clone(req.Context())
	out.URL.Scheme = t.internal.Scheme
	out.URL.Host = t.internal.Host
	out.Host = ""
	out.Header.Set(InstanceHostHeader, t.publicHost)
	out.Header.Set(PublicHostHeader, t.publicHost)
	return t.next.RoundTrip(out)
}

// GRPC returns the address the gRPC clients dial and the connection options
// that go with it: the internal address in plain text with both instance
// headers, or the issuer's host over TLS when there is no internal address.
func (r Route) GRPC() (endpoint string, opts []zitadelconn.Option) {
	if r.internal == nil {
		return hostPort(r.issuer), nil
	}
	opts = []zitadelconn.Option{
		zitadelconn.WithTransportHeader(InstanceHostHeader, r.issuer.Host),
		zitadelconn.WithTransportHeader(PublicHostHeader, r.issuer.Host),
	}
	if r.internal.Scheme == "http" {
		opts = append(opts, zitadelconn.WithInsecure())
	}
	return hostPort(r.internal), opts
}

// hostPort returns the host:port of u, with the scheme's default port when u
// names none.
func hostPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return u.Host
	}
	if u.Scheme == "http" {
		return u.Hostname() + ":80"
	}
	return u.Hostname() + ":443"
}
