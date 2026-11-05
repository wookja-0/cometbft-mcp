// Package safehttp is the only way cometbft-mcp reaches the network.
//
// It is the security boundary of the server: every request is a GET to an
// endpoint from a fixed allowlist, with an allowlisted set of query
// parameters, no redirects, a timeout, a response size cap and a global
// concurrency cap. Endpoints are values of an unexported-field type, so code
// outside this package cannot invent a new path — adding one means editing
// the allowlist below, in review.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Endpoint is an allowlisted request path. Only this package can construct one.
type Endpoint struct {
	path    string
	params  []string       // allowed query parameter names
	segment *regexp.Regexp // if set, one trailing path segment matching this is required
}

// Path returns the request path, e.g. "/status".
func (e Endpoint) Path() string { return e.path }

// CometBFT RPC endpoints (URI-over-HTTP form of JSON-RPC). Everything here
// is a read; nothing mutates node or chain state. /dump_consensus_state is
// deliberately absent: it lists every peer's address.
var (
	CometHealth     = Endpoint{path: "/health"}
	CometStatus     = Endpoint{path: "/status"}
	CometNetInfo    = Endpoint{path: "/net_info"}
	CometABCIInfo   = Endpoint{path: "/abci_info"}
	CometValidators = Endpoint{path: "/validators", params: []string{"height", "page", "per_page"}}
	CometCommit     = Endpoint{path: "/commit", params: []string{"height"}}
	CometConsensus  = Endpoint{path: "/consensus_state"}
)

// Cosmos SDK REST (gRPC-gateway) query endpoints.
var (
	bech32Segment = regexp.MustCompile(`^[a-z0-9]{1,90}$`)
	page          = []string{"pagination.limit", "pagination.key"}

	CosmosNodeInfo       = Endpoint{path: "/cosmos/base/tendermint/v1beta1/node_info"}
	CosmosBech32Prefix   = Endpoint{path: "/cosmos/auth/v1beta1/bech32"}
	CosmosSlashingParams = Endpoint{path: "/cosmos/slashing/v1beta1/params"}
	CosmosSigningInfo    = Endpoint{path: "/cosmos/slashing/v1beta1/signing_infos/", segment: bech32Segment}
	CosmosValidators     = Endpoint{path: "/cosmos/staking/v1beta1/validators", params: page}
	CosmosUpgradePlan    = Endpoint{path: "/cosmos/upgrade/v1beta1/current_plan"}
	CosmosProposals      = Endpoint{path: "/cosmos/gov/v1/proposals", params: append([]string{"proposal_status"}, page...)}
)

var allowlist = map[string]Endpoint{}

func init() {
	for _, e := range []Endpoint{
		CometHealth, CometStatus, CometNetInfo, CometABCIInfo, CometValidators, CometCommit, CometConsensus,
		CosmosNodeInfo, CosmosBech32Prefix, CosmosSlashingParams, CosmosSigningInfo, CosmosValidators, CosmosUpgradePlan, CosmosProposals,
	} {
		allowlist[e.path] = e
	}
}

// deniedPrefixes are paths that mutate state or the node's p2p layer. None of
// them is in the allowlist; this is a second, explicit line of defense that a
// future allowlist edit cannot silently undo.
var deniedPrefixes = []string{"/broadcast_", "/unsafe_", "/dial_", "/check_tx", "/dump_consensus_state", "/cosmos/tx/"}

// Error codes. Error messages never carry the node URL or the raw transport
// error, because both can contain internal addresses.
const (
	CodeNotAllowed   = "endpoint_not_allowed"
	CodeUnreachable  = "rpc_unreachable"
	CodeTimeout      = "rpc_timeout"
	CodeHTTPStatus   = "rpc_http_status"
	CodeBodyTooLarge = "rpc_body_too_large"
	CodeCanceled     = "canceled"
)

// Error is a sanitized request failure.
type Error struct {
	Code   string
	Detail string // safe to show; never contains a URL or address
	cause  error  // raw error, for --reveal-network only
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// Raw returns the unsanitized cause. Only call it when the operator opted in
// to revealing network details.
func (e *Error) Raw() error { return e.cause }

// Options configure a Client. Zero values pick the defaults.
type Options struct {
	Timeout       time.Duration // per request; default 10s
	MaxBodyBytes  int64         // default 8 MiB
	MaxConcurrent int           // in-flight requests across all nodes; default 4
}

// Client issues allowlisted GET requests.
type Client struct {
	http    *http.Client
	maxBody int64
	sem     chan struct{}
}

func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 8 << 20
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 4
	}
	return &Client{
		http: &http.Client{
			Timeout: opts.Timeout,
			// A redirect could point at another path or host; never follow one.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		maxBody: opts.MaxBodyBytes,
		sem:     make(chan struct{}, opts.MaxConcurrent),
	}
}

// Get fetches endpoint e relative to base with the given query parameters.
func (c *Client) Get(ctx context.Context, base *url.URL, e Endpoint, query url.Values) ([]byte, error) {
	return c.GetSegment(ctx, base, e, "", query)
}

// GetSegment is Get for endpoints that take one path parameter, such as a
// consensus address. The segment must match the endpoint's pattern.
func (c *Client) GetSegment(ctx context.Context, base *url.URL, e Endpoint, segment string, query url.Values) ([]byte, error) {
	if err := check(e, segment, query); err != nil {
		return nil, err
	}

	u := *base
	u.Path = strings.TrimSuffix(base.Path, "/") + e.path + segment
	u.RawPath = ""
	u.RawQuery = query.Encode()
	u.Fragment = ""

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, &Error{Code: CodeCanceled, cause: ctx.Err()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &Error{Code: CodeUnreachable, Detail: "invalid request", cause: err}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classify(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, classify(err)
	}
	if int64(len(body)) > c.maxBody {
		return nil, &Error{Code: CodeBodyTooLarge, Detail: fmt.Sprintf("response exceeds %d bytes", c.maxBody)}
	}
	// CometBFT reports JSON-RPC errors (e.g. a pruned height) with a 500 and
	// a JSON body; hand those to the caller to decode.
	if resp.StatusCode != http.StatusOK && !isJSON(resp) {
		return nil, &Error{Code: CodeHTTPStatus, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return body, nil
}

func check(e Endpoint, segment string, query url.Values) error {
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(e.path, p) {
			return &Error{Code: CodeNotAllowed, Detail: e.path}
		}
	}
	allowed, ok := allowlist[e.path]
	if !ok {
		return &Error{Code: CodeNotAllowed, Detail: e.path}
	}
	switch {
	case allowed.segment == nil && segment != "":
		return &Error{Code: CodeNotAllowed, Detail: e.path + " takes no path parameter"}
	case allowed.segment != nil && !allowed.segment.MatchString(segment):
		return &Error{Code: CodeNotAllowed, Detail: "invalid path parameter for " + e.path}
	}
	for k := range query {
		if !contains(allowed.params, k) {
			return &Error{Code: CodeNotAllowed, Detail: fmt.Sprintf("query parameter %q on %s", k, e.path)}
		}
	}
	return nil
}

func classify(err error) *Error {
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Code: CodeCanceled, cause: err}
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return &Error{Code: CodeTimeout, cause: err}
	default:
		return &Error{Code: CodeUnreachable, cause: err}
	}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func isJSON(resp *http.Response) bool {
	return strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
