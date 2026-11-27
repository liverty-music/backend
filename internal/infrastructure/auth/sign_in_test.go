package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/authn"
	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

// fakeSignInService is the sign-in service of these tests: it serves its keys
// and answers introspection for the tokens marked active. While down, an
// introspection ask gets its connection closed; while hung, it gets no
// answer. Its keys are always served, so a test reaches introspection.
type fakeSignInService struct {
	t       *testing.T
	server  *httptest.Server
	key     *rsa.PrivateKey
	keySet  jwk.Set
	onAsk   func() // runs inside each answered ask
	down    atomic.Bool
	hung    atomic.Bool
	asks    atomic.Int32
	release chan struct{}

	mu     sync.Mutex
	active map[string]bool
}

func newFakeSignInService(t *testing.T) *fakeSignInService {
	t.Helper()
	key, keySet := newKeySet(t)
	f := &fakeSignInService{t: t, key: key, keySet: keySet, active: map[string]bool{}, release: make(chan struct{})}
	f.server = httptest.NewServer(f)
	t.Cleanup(func() {
		close(f.release)
		f.server.Close()
	})
	return f
}

func (f *fakeSignInService) issuer() string { return f.server.URL }

func (f *fakeSignInService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/v2/keys":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.keySet)
	case "/oauth/v2/introspect":
		f.asks.Add(1)
		if f.down.Load() {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		if f.hung.Load() {
			select {
			case <-r.Context().Done():
			case <-f.release:
			}
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" ||
			claimOf(r.PostForm.Get("client_assertion"), "aud") == nil {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		if f.onAsk != nil {
			f.onAsk()
		}
		f.mu.Lock()
		active := f.active[r.PostForm.Get("token")]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"active": active})
	default:
		http.NotFound(w, r)
	}
}

// claimOf returns a claim of a JWT without verifying it.
func claimOf(token, name string) any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims[name]
}

// token issues a token for the audience that expires after ttl; when active,
// the sign-in service reports it active.
func (f *fakeSignInService) token(audience string, ttl time.Duration, active bool) string {
	f.t.Helper()
	tok := jwt.New()
	require.NoError(f.t, tok.Set(jwt.IssuerKey, f.issuer()))
	require.NoError(f.t, tok.Set(jwt.SubjectKey, "user-1"))
	require.NoError(f.t, tok.Set(jwt.AudienceKey, []string{audience, "client-id-of-the-app"}))
	require.NoError(f.t, tok.Set("email", "fan@example.com"))
	require.NoError(f.t, tok.Set(jwt.JwtIDKey, rand.Text()))
	require.NoError(f.t, tok.Set(jwt.IssuedAtKey, time.Now()))
	require.NoError(f.t, tok.Set(jwt.ExpirationKey, time.Now().Add(ttl)))
	key, err := jwk.Import[jwk.Key](f.key)
	require.NoError(f.t, err)
	require.NoError(f.t, key.Set(jwk.KeyIDKey, "test-key-id"))
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), key))
	require.NoError(f.t, err)
	s := string(signed)
	f.mu.Lock()
	f.active[s] = active
	f.mu.Unlock()
	return s
}

// writeAPIKey writes a ZITADEL API application key and returns its path.
func writeAPIKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	data, err := json.Marshal(map[string]string{
		"type":     "application",
		"keyId":    "api-key-1",
		"key":      string(keyPEM),
		"appId":    "app-1",
		"clientId": "backend-api@project",
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "api-key.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// api is one signed-in API under test, guarded as the server wires it.
type api struct {
	name      string
	audience  string
	other     string // the project of another app
	public    string // a procedure open to everyone
	signedIn  string // a procedure that needs a sign-in
	publicMap map[string]bool
}

var apis = []api{
	{
		name: "fan API", audience: "product-project", other: "organizer-console-project",
		public: "/liverty_music.rpc.concert.v1.ConcertService/Get", signedIn: "/liverty_music.rpc.user.v1.UserService/Get",
		publicMap: auth.FanPublicProcedures(),
	},
	{
		name: "admin console API", audience: "admin-console-project", other: "product-project",
		signedIn: "/liverty_music.rpc.concert.v1.ConcertModerationService/List",
	},
	{
		name: "organizer API", audience: "organizer-console-project", other: "product-project",
		public: "/liverty_music.rpc.organizer.v1.OrganizerService/SendSignInLinks", signedIn: "/liverty_music.rpc.organizer.v1.OrganizerService/Get",
		publicMap: auth.OrganizerPublicProcedures(),
	},
}

// guarded is an API server's authentication in front of a handler that
// records whether a call reached it.
type guarded struct {
	f            *fakeSignInService
	introspector *auth.Introspector
	handler      http.Handler
	reached      atomic.Int32
	guest        atomic.Int32
	clock        *fakeClock
}

func newGuarded(t *testing.T, a api) *guarded {
	t.Helper()
	f := newFakeSignInService(t)
	validator, err := auth.NewJWTValidator(f.issuer(), f.issuer()+"/oauth/v2/keys", 15*time.Minute, http.DefaultClient)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })
	introspector, err := auth.NewIntrospector(f.issuer(), writeAPIKey(t), http.DefaultTransport)
	require.NoError(t, err)
	g := &guarded{f: f, introspector: introspector, clock: &fakeClock{now: time.Now()}}
	auth.SetIntrospectorClock(introspector, g.clock.Now)
	checker := auth.NewSignInChecker(validator, a.audience, introspector)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.reached.Add(1)
		if authn.GetInfo(r.Context()) == nil {
			g.guest.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	})
	g.handler = authn.NewMiddleware(auth.NewAuthFunc(checker, a.publicMap)).Wrap(inner)
	return g
}

// call sends a Connect unary call to procedure with token and returns the
// HTTP status and the Connect error code ("" on success).
func (g *guarded) call(t *testing.T, procedure, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, procedure, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	g.handler.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	return rec.Code, e.Code
}

// @spec components/adapter/fan/api/rpc/sign-in "Access token"
// @spec components/adapter/admin/api/rpc/sign-in "Access token"
// @spec components/adapter/organizer/api/rpc/sign-in "Access token"
func TestSignIn_AccessToken(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)

			_, code := g.call(t, a.signedIn, g.f.token(a.audience, time.Hour, true))

			assert.Empty(t, code)
			assert.Equal(t, int32(1), g.reached.Load(), "the call is handled")
			assert.Zero(t, g.guest.Load(), "as the signed-in caller")
		})
	}
}

// An ID token is signed by the sign-in service and names the app, but the
// sign-in service does not report it active.
//
// @spec components/adapter/fan/api/rpc/sign-in "ID token"
// @spec components/adapter/admin/api/rpc/sign-in "ID token"
// @spec components/adapter/organizer/api/rpc/sign-in "ID token"
func TestSignIn_IDToken(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)

			_, code := g.call(t, a.signedIn, g.f.token(a.audience, 12*time.Hour, false))

			assert.Equal(t, "unauthenticated", code)
			assert.Zero(t, g.reached.Load(), "nothing is read or changed")
		})
	}
}

// A removed identity's unexpired access token is reported inactive once the
// answer about it from before the removal (at most 60 seconds) is no longer
// reused.
//
// @spec components/adapter/fan/api/rpc/sign-in "Removed identity"
// @spec components/adapter/admin/api/rpc/sign-in "Removed identity"
// @spec components/adapter/organizer/api/rpc/sign-in "Removed identity"
func TestSignIn_RemovedIdentity(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)
			token := g.f.token(a.audience, time.Hour, true)
			_, code := g.call(t, a.signedIn, token)
			require.Empty(t, code, "signed in before the removal")

			g.f.mu.Lock()
			g.f.active[token] = false // the identity is removed
			g.f.mu.Unlock()
			g.clock.Advance(61 * time.Second)
			_, code = g.call(t, a.signedIn, token)

			assert.Equal(t, "unauthenticated", code)
			assert.Equal(t, int32(1), g.reached.Load(), "only the call before the removal is handled")
		})
	}
}

// @spec components/adapter/fan/api/rpc/sign-in "Token of another app"
// @spec components/adapter/admin/api/rpc/sign-in "Token of another app"
// @spec components/adapter/organizer/api/rpc/sign-in "Token of another app"
func TestSignIn_TokenOfAnotherApp(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)

			_, code := g.call(t, a.signedIn, g.f.token(a.other, time.Hour, true))

			assert.Equal(t, "permission_denied", code)
			assert.Zero(t, g.reached.Load(), "nothing is read or changed")
			assert.Zero(t, g.f.asks.Load(), "a foreign token never reaches the sign-in service")
		})
	}
}

// @spec components/adapter/fan/api/rpc/sign-in "Calls within a minute"
// @spec components/adapter/admin/api/rpc/sign-in "Calls within a minute"
// @spec components/adapter/organizer/api/rpc/sign-in "Calls within a minute"
func TestSignIn_CallsWithinAMinute(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)
			token := g.f.token(a.audience, time.Hour, true)

			_, first := g.call(t, a.signedIn, token)
			g.f.down.Store(true)
			g.clock.Advance(10 * time.Second)
			_, second := g.call(t, a.signedIn, token)

			assert.Empty(t, first)
			assert.Empty(t, second)
			assert.Equal(t, int32(2), g.reached.Load(), "both calls are handled")
			assert.Equal(t, int32(1), g.f.asks.Load(), "the second call reuses the answer")
		})
	}
}

// @spec components/adapter/fan/api/rpc/sign-in "Sign-in service unreachable"
// @spec components/adapter/admin/api/rpc/sign-in "Sign-in service unreachable"
// @spec components/adapter/organizer/api/rpc/sign-in "Sign-in service unreachable"
func TestSignIn_SignInServiceUnreachable(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)
			// Another caller's call loads the keys, so this one fails only at
			// introspection.
			_, warm := g.call(t, a.signedIn, g.f.token(a.audience, time.Hour, true))
			require.Empty(t, warm)
			g.f.down.Store(true)

			_, code := g.call(t, a.signedIn, g.f.token(a.audience, time.Hour, true))

			assert.Equal(t, "unavailable", code)
			assert.Equal(t, int32(1), g.reached.Load(), "nothing is read or changed")
		})
	}
}

// @spec components/adapter/fan/api/rpc/sign-in "Sign-in service not answering"
// @spec components/adapter/admin/api/rpc/sign-in "Sign-in service not answering"
// @spec components/adapter/organizer/api/rpc/sign-in "Sign-in service not answering"
func TestSignIn_SignInServiceNotAnswering(t *testing.T) {
	t.Parallel()
	require.Equal(t, 5*time.Second, auth.IntrospectionTimeout, "one ask waits at most 5 seconds")
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)
			_, warm := g.call(t, a.signedIn, g.f.token(a.audience, time.Hour, true))
			require.Empty(t, warm)
			// The bound is shortened so the test does not wait 5 seconds; the
			// assertion above pins the real value.
			auth.SetIntrospectorTimeout(g.introspector, 200*time.Millisecond)
			g.f.hung.Store(true)

			start := time.Now()
			_, code := g.call(t, a.signedIn, g.f.token(a.audience, time.Hour, true))

			assert.Equal(t, "unavailable", code)
			assert.Less(t, time.Since(start), 2*time.Second, "the call does not wait for the sign-in service")
			assert.Equal(t, int32(1), g.reached.Load())
		})
	}
}

// The token expires after the sign-in service reported it active; the
// reused answer does not outlive it.
//
// @spec components/adapter/fan/api/rpc/sign-in "Expired token"
// @spec components/adapter/admin/api/rpc/sign-in "Expired token"
// @spec components/adapter/organizer/api/rpc/sign-in "Expired token"
func TestSignIn_ExpiredToken(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)
			token := g.f.token(a.audience, 2*time.Second, true)
			_, first := g.call(t, a.signedIn, token)
			require.Empty(t, first)

			time.Sleep(3 * time.Second) // the token's exp is checked against the real clock
			g.clock.Advance(10 * time.Second)
			_, code := g.call(t, a.signedIn, token)

			assert.Equal(t, "unauthenticated", code)
			assert.Equal(t, int32(1), g.reached.Load())
		})
	}
}

// A refused token on a call open to everyone makes the caller a guest.
//
// @spec components/adapter/fan/api/rpc/sign-in "Refused token on a public call"
// @spec components/adapter/organizer/api/rpc/sign-in "Refused token on a public call"
func TestSignIn_RefusedTokenOnAPublicCall(t *testing.T) {
	t.Parallel()
	for _, a := range apis {
		if a.public == "" {
			continue // the admin console API has no call open to everyone
		}
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			g := newGuarded(t, a)

			_, code := g.call(t, a.public, g.f.token(a.audience, 12*time.Hour, false))

			assert.Empty(t, code)
			assert.Equal(t, int32(1), g.reached.Load(), "the call is handled")
			assert.Equal(t, int32(1), g.guest.Load(), "as without a sign-in")
		})
	}
}

// An answer is reused for 60 seconds from when the ask was sent, not from
// when the answer arrived.
func TestIntrospector_ReuseCountedFromTheAsk(t *testing.T) {
	t.Parallel()
	a := apis[0]
	g := newGuarded(t, a)
	g.f.onAsk = func() { g.clock.Advance(30 * time.Second) } // a slow answer
	token := g.f.token(a.audience, time.Hour, true)

	_, code := g.call(t, a.signedIn, token) // asked at T, answered at T+30s
	require.Empty(t, code)
	g.f.onAsk = nil
	g.clock.Advance(29 * time.Second) // T+59s
	_, code = g.call(t, a.signedIn, token)
	require.Empty(t, code)
	assert.Equal(t, int32(1), g.f.asks.Load(), "reused at T+59s")

	g.clock.Advance(2 * time.Second) // T+61s, only 31s after the answer
	_, code = g.call(t, a.signedIn, token)
	require.Empty(t, code)
	assert.Equal(t, int32(2), g.f.asks.Load(), "asked again at T+61s")
}

// A refused client assertion is our configuration, not the caller's sign-in.
func TestIntrospector_RefusedClient(t *testing.T) {
	t.Parallel()
	f := newFakeSignInService(t)
	introspector, err := auth.NewIntrospector(f.server.URL+"/elsewhere", writeAPIKey(t), http.DefaultTransport)
	require.NoError(t, err)
	_, err = introspector.Active(t.Context(), "token", time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, apperr.ErrInternal)
}
