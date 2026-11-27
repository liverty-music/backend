package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	oidcclient "github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/zitadel-go/v3/pkg/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/singleflight"
)

const (
	// introspectionReuse is how long an answer that a token is active is
	// reused, counted from when the ask was sent (RFC 7662 section 4).
	introspectionReuse = 60 * time.Second

	// introspectionTimeout bounds one ask. The authentication layer runs
	// before the server's handler timeout, so nothing else bounds an ask that
	// the sign-in service accepts and never answers.
	introspectionTimeout = 5 * time.Second

	// introspectionCacheSize bounds the reused answers held at once.
	introspectionCacheSize = 10_000

	// assertionLifetime is the lifetime of the JWT that authenticates an ask.
	assertionLifetime = time.Minute
)

// Introspector asks the sign-in service (ZITADEL) whether an access token is
// active (RFC 7662 introspection), authenticated as an API application with
// a private-key JWT. ZITADEL answers only about tokens issued for that
// application's project.
type Introspector struct {
	endpoint string
	issuer   string
	clientID string
	signer   jose.Signer
	http     *http.Client
	now      func() time.Time
	timeout  time.Duration

	answers *lru.Cache[[sha256.Size]byte, time.Time]
	asks    singleflight.Group

	duration metric.Float64Histogram
	lookups  metric.Int64Counter
}

// NewIntrospector returns an Introspector for the sign-in service with the
// public issuer URL issuer, authenticated with the API application key at
// keyPath. transport carries the asks (route.Route.Transport sends them over
// the cluster network); the endpoint and the assertion's audience stay the
// public issuer. Construction makes no network request.
func NewIntrospector(issuer, keyPath string, transport http.RoundTripper) (*Introspector, error) {
	key, err := client.ConfigFromKeyFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read introspection key: %w", err)
	}
	if key.ClientID == "" {
		return nil, fmt.Errorf("introspection key %s has no clientId: want an API application key", keyPath)
	}
	signer, err := oidcclient.NewSignerFromPrivateKeyByte([]byte(key.Key), key.KeyID)
	if err != nil {
		return nil, fmt.Errorf("parse introspection key: %w", err)
	}
	return newIntrospector(issuer, key.ClientID, signer, transport, time.Now)
}

// newIntrospector builds an Introspector around a signer and a clock.
func newIntrospector(issuer, clientID string, signer jose.Signer, transport http.RoundTripper, now func() time.Time) (*Introspector, error) {
	answers, err := lru.New[[sha256.Size]byte, time.Time](introspectionCacheSize)
	if err != nil {
		return nil, err
	}
	meter := otel.Meter("liverty-music/backend/auth")
	duration, _ := meter.Float64Histogram("sign_in.introspection.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time of one introspection ask to the sign-in service, by outcome"),
		// The default bounds suit milliseconds: in seconds every ask falls
		// into one bucket. 0.3 is the p95 the ask must stay under, and 5 is
		// its timeout.
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.25, 0.3, 0.4, 0.5, 0.75, 1, 2.5, 5),
	)
	lookups, _ := meter.Int64Counter("sign_in.introspection.lookups",
		metric.WithDescription("Token checks, by whether a reused answer served them (hit) or the sign-in service was asked (miss)"),
	)
	issuer = strings.TrimSuffix(issuer, "/")
	return &Introspector{
		endpoint: issuer + "/oauth/v2/introspect",
		issuer:   issuer,
		clientID: clientID,
		signer:   signer,
		http:     &http.Client{Transport: transport},
		now:      now,
		timeout:  introspectionTimeout,
		answers:  answers,
		duration: duration,
		lookups:  lookups,
	}, nil
}

// Active reports whether token, which expires at expiry, is active. An
// answer that it is active is reused for 60 seconds from when the ask was
// sent and never past expiry; an inactive answer and a failure are not
// reused. Concurrent checks of one token share one ask.
//
// # Possible errors
//
//   - Unavailable: the sign-in service could not be reached, answered a
//     server error, or did not answer within 5 seconds.
//   - Internal: the sign-in service refused the client assertion or answered
//     something that is not an introspection response (our configuration).
func (i *Introspector) Active(ctx context.Context, token string, expiry time.Time) (bool, error) {
	key := sha256.Sum256([]byte(token))
	if until, ok := i.answers.Get(key); ok && i.now().Before(until) {
		i.lookups.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "hit")))
		return true, nil
	}
	i.lookups.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "miss")))

	v, err, _ := i.asks.Do(string(key[:]), func() (any, error) {
		// One caller's cancellation must not fail the others sharing the ask.
		askCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), i.timeout)
		defer cancel()
		sent := i.now()
		active, err := i.ask(askCtx, token)
		i.record(askCtx, sent, active, err)
		if err != nil {
			return false, err
		}
		if active {
			i.answers.Add(key, earlier(sent.Add(introspectionReuse), expiry))
		}
		return active, nil
	})
	if err != nil {
		return false, err
	}
	return v.(bool), nil
}

// earlier returns the earlier of a and b.
func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// record measures one ask.
func (i *Introspector) record(ctx context.Context, sent time.Time, active bool, err error) {
	outcome := "inactive"
	switch {
	case err != nil:
		outcome = "error"
	case active:
		outcome = "active"
	}
	i.duration.Record(ctx, i.now().Sub(sent).Seconds(), metric.WithAttributes(attribute.String("outcome", outcome)))
}

// ask sends one introspection request.
func (i *Introspector) ask(ctx context.Context, token string) (bool, error) {
	assertion, err := oidcclient.SignedJWTProfileAssertion(i.clientID, []string{i.issuer}, assertionLifetime, i.signer)
	if err != nil {
		return false, apperr.Wrap(err, codes.Internal, "sign the introspection client assertion")
	}
	form := url.Values{
		"token":                 {token},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false, apperr.Wrap(err, codes.Internal, "build the introspection request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := i.http.Do(req)
	if err != nil {
		return false, apperr.Wrap(err, codes.Unavailable, "the sign-in service cannot be asked about a token")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, apperr.Wrap(err, codes.Unavailable, "read the introspection answer")
	}

	switch {
	case resp.StatusCode >= http.StatusInternalServerError:
		return false, apperr.New(codes.Unavailable, fmt.Sprintf("the sign-in service answered %s to an introspection", resp.Status))
	case resp.StatusCode != http.StatusOK:
		return false, apperr.New(codes.Internal, fmt.Sprintf("the sign-in service refused the introspection client: %s", resp.Status))
	}
	var answer struct {
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Active == nil {
		return false, apperr.New(codes.Internal, "the sign-in service answered an introspection without `active`")
	}
	return *answer.Active, nil
}
