// Package httpx provides lightweight HTTP probes used by background jobs.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	// defaultLivenessTimeout bounds a single liveness probe. Kept short: the
	// merch job probes one URL per in-window series and a slow host should not
	// stall the run.
	defaultLivenessTimeout = 10 * time.Second

	// livenessUserAgent is sent on probes so hosts that block default Go
	// clients are more likely to answer; a block is still treated as ambiguous
	// (alive), never as a dead link.
	livenessUserAgent = "LivertyMusicBot/1.0 (+https://liverty-music.app; merch-liveness-check)"
)

// LivenessChecker probes a URL with a single GET and classifies the outcome as
// definitively dead or ambiguous/alive. It implements [entity.MerchLivenessChecker].
type LivenessChecker struct {
	client *http.Client
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.MerchLivenessChecker = (*LivenessChecker)(nil)

// NewLivenessChecker builds a checker. A nil client gets an SSRF-hardened
// default (private/loopback/link-local/metadata addresses are refused at
// connect time) with the liveness timeout and OTel instrumentation applied —
// this is the production path. A non-nil client is used verbatim, which lets
// tests point the checker at an httptest server on loopback.
func NewLivenessChecker(client *http.Client, logger *logging.Logger) *LivenessChecker {
	if client == nil {
		client = newSafeClient()
	}
	return &LivenessChecker{client: client, logger: logger}
}

// newSafeClient builds the production HTTP client. Its dialer's Control hook
// runs AFTER DNS resolution with the concrete IP that will be dialed, so it
// blocks both literal private addresses and public hostnames that resolve to
// private space (DNS-rebinding). The merch liveness probe fetches a
// Gemini-resolved, only-soft-trusted URL from inside the cluster, so this guard
// keeps it from reaching the GCP metadata server (169.254.169.254), loopback,
// or RFC1918 services.
func newSafeClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: defaultLivenessTimeout,
		Control: blockNonPublicAddr,
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   defaultLivenessTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   defaultLivenessTimeout,
		Transport: otelhttp.NewTransport(transport),
	}
}

// blockNonPublicAddr is a net.Dialer Control hook that refuses to connect to a
// non-public IP. address is "host:port" with host already resolved to an IP.
func blockNonPublicAddr(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf guard: unresolved dial address %q", address)
	}
	if isNonPublicIP(ip) {
		return fmt.Errorf("ssrf guard: refusing to dial non-public address %s", ip)
	}
	return nil
}

// isNonPublicIP reports whether ip is in a range a public merch URL must never
// resolve to: loopback, RFC1918 / ULA private, link-local (incl. the
// 169.254.169.254 cloud-metadata endpoint), unspecified, or multicast.
func isNonPublicIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// ambiguousStatuses are 4xx codes that do NOT prove a link is gone: they
// indicate bot-blocking, auth walls, or rate-limiting, which a real browser /
// logged-in fan would sail past. Treating them as dead would churn live links
// and needlessly re-bill Gemini, so they are classified alive.
var ambiguousStatuses = map[int]struct{}{
	http.StatusUnauthorized:    {}, // 401 — auth wall, page likely still exists
	http.StatusForbidden:       {}, // 403 — common bot block
	http.StatusRequestTimeout:  {}, // 408 — transient
	http.StatusTooManyRequests: {}, // 429 — rate limit, transient
}

// IsDeadLink reports whether url is definitively dead. It returns true only for
// a definitive client-side "gone" status (4xx other than the ambiguous set) or
// a hard, non-timeout transport failure. 2xx/3xx, 5xx (server-side, transient),
// ambiguous 4xx, and timeouts all return false so a live link is never cleared
// on flaky evidence.
func (c *LivenessChecker) IsDeadLink(ctx context.Context, url string) bool {
	// A short per-probe deadline on top of the client timeout guards against a
	// client constructed without one.
	probeCtx, cancel := context.WithTimeout(ctx, defaultLivenessTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		// A malformed stored URL is handled by the URL-validation path, not by
		// liveness; do not treat it as a dead link here.
		c.debug(ctx, "liveness: skipping malformed URL", url, err)
		return false
	}
	req.Header.Set("User-Agent", livenessUserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		if isTimeout(err) {
			// Transient: leave the link alone.
			c.debug(ctx, "liveness: transient timeout, treating as alive", url, err)
			return false
		}
		// Hard failure (DNS no-such-host, connection refused, TLS failure):
		// the host is genuinely unreachable → dead.
		c.debug(ctx, "liveness: hard transport failure, treating as dead", url, err)
		return true
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return false
	}
	if _, ambiguous := ambiguousStatuses[resp.StatusCode]; ambiguous {
		return false
	}
	// 5xx is server-side and usually transient; only definitive client-side
	// "gone" statuses (the remaining 4xx, e.g. 404 / 410) count as dead.
	if resp.StatusCode >= 500 {
		c.debug(ctx, "liveness: 5xx treated as transient/alive", url, nil)
		return false
	}
	return true
}

// isTimeout reports whether err is a deadline/timeout rather than a hard
// failure. context cancellation and deadline-exceeded count as timeouts.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func (c *LivenessChecker) debug(ctx context.Context, msg, url string, err error) {
	if c.logger == nil {
		return
	}
	attrs := []slog.Attr{slog.String("url", url)}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	c.logger.Debug(ctx, msg, attrs...)
}
