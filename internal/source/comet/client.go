// Package comet reads a CometBFT node over its RPC. It only decodes the
// fields cometbft-mcp uses, so it does not depend on the cometbft module.
// Supported: CometBFT v0.34 – v0.39 (the fields below are stable across them).
package comet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/source/jsonnum"
)

// Error codes for failures past the transport layer.
const (
	CodeBadResponse       = "rpc_bad_response"
	CodeRPCError          = "rpc_error"
	CodeHeightUnavailable = "height_unavailable"
)

// Error is a decoded JSON-RPC error or an undecodable response.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// Int64 decodes CometBFT's int64 fields, which are JSON strings.
type Int64 = jsonnum.Int64

// BlockIDFlag values in a commit signature.
const (
	FlagAbsent = 1
	FlagCommit = 2
	FlagNil    = 3
)

type NodeInfo struct {
	ID         string `json:"id"`
	ListenAddr string `json:"listen_addr"`
	Network    string `json:"network"`
	Version    string `json:"version"`
	Moniker    string `json:"moniker"`
}

type Status struct {
	NodeInfo NodeInfo `json:"node_info"`
	SyncInfo struct {
		LatestBlockHeight   Int64     `json:"latest_block_height"`
		LatestBlockTime     time.Time `json:"latest_block_time"`
		EarliestBlockHeight Int64     `json:"earliest_block_height"`
		CatchingUp          bool      `json:"catching_up"`
	} `json:"sync_info"`
	ValidatorInfo struct {
		Address     string `json:"address"`
		VotingPower Int64  `json:"voting_power"`
	} `json:"validator_info"`
}

type Peer struct {
	NodeInfo         NodeInfo `json:"node_info"`
	IsOutbound       bool     `json:"is_outbound"`
	RemoteIP         string   `json:"remote_ip"`
	ConnectionStatus struct {
		Duration Int64 `json:"Duration"` // nanoseconds
	} `json:"connection_status"`
}

type NetInfo struct {
	Listening bool     `json:"listening"`
	Listeners []string `json:"listeners"`
	NPeers    Int64    `json:"n_peers"`
	Peers     []Peer   `json:"peers"`
}

type ABCIInfo struct {
	Response struct {
		Data            string `json:"data"`
		Version         string `json:"version"`
		AppVersion      Int64  `json:"app_version"`
		LastBlockHeight Int64  `json:"last_block_height"`
	} `json:"response"`
}

type Validator struct {
	Address     string `json:"address"`
	VotingPower Int64  `json:"voting_power"`
}

type CommitSig struct {
	BlockIDFlag      int       `json:"block_id_flag"`
	ValidatorAddress string    `json:"validator_address"`
	Timestamp        time.Time `json:"timestamp"`
}

type Commit struct {
	Header struct {
		Height         Int64     `json:"height"`
		Time           time.Time `json:"time"`
		ValidatorsHash string    `json:"validators_hash"`
	} `json:"header"`
	Commit struct {
		Height     Int64       `json:"height"`
		Signatures []CommitSig `json:"signatures"`
	} `json:"commit"`
}

// Client reads one node.
type Client struct {
	http *safehttp.Client
	base *url.URL
}

func NewClient(h *safehttp.Client, base *url.URL) *Client {
	return &Client{http: h, base: base}
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	var out Status
	return &out, c.call(ctx, safehttp.CometStatus, nil, &out)
}

func (c *Client) NetInfo(ctx context.Context) (*NetInfo, error) {
	var out NetInfo
	return &out, c.call(ctx, safehttp.CometNetInfo, nil, &out)
}

func (c *Client) ABCIInfo(ctx context.Context) (*ABCIInfo, error) {
	var out ABCIInfo
	return &out, c.call(ctx, safehttp.CometABCIInfo, nil, &out)
}

func (c *Client) Commit(ctx context.Context, height int64) (*Commit, error) {
	var out struct {
		SignedHeader Commit `json:"signed_header"`
	}
	q := url.Values{"height": {strconv.FormatInt(height, 10)}}
	if err := c.call(ctx, safehttp.CometCommit, q, &out); err != nil {
		return nil, err
	}
	return &out.SignedHeader, nil
}

// maxValidatorPages bounds /validators paging (100 per page).
const maxValidatorPages = 20

// Validators returns the full validator set at height (0 = latest), in
// CometBFT's canonical order — the same order as a commit's signatures.
func (c *Client) Validators(ctx context.Context, height int64) ([]Validator, error) {
	var all []Validator
	for page := 1; page <= maxValidatorPages; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
		if height > 0 {
			q.Set("height", strconv.FormatInt(height, 10))
		}
		var out struct {
			Validators []Validator `json:"validators"`
			Total      Int64       `json:"total"`
		}
		if err := c.call(ctx, safehttp.CometValidators, q, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Validators...)
		if len(out.Validators) == 0 || int64(len(all)) >= int64(out.Total) {
			return all, nil
		}
	}
	return nil, &Error{Code: CodeBadResponse, Detail: fmt.Sprintf("validator set larger than %d", maxValidatorPages*100)}
}

func (c *Client) call(ctx context.Context, e safehttp.Endpoint, q url.Values, out any) error {
	body, err := c.http.Get(ctx, c.base, e, q)
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return &Error{Code: CodeBadResponse, Detail: e.Path() + " did not return JSON-RPC (is this a CometBFT RPC URL?)"}
	}
	if env.Error != nil {
		return rpcError(env.Error.Message, env.Error.Data)
	}
	if len(env.Result) == 0 {
		return &Error{Code: CodeBadResponse, Detail: e.Path() + " returned no result"}
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return &Error{Code: CodeBadResponse, Detail: e.Path() + ": " + err.Error()}
	}
	return nil
}

func rpcError(message, data string) *Error {
	detail := message
	if data != "" {
		detail = data
	}
	if len(detail) > 200 {
		detail = detail[:200] + "…"
	}
	if strings.Contains(data, "is not available") || strings.Contains(data, "must be less than or equal to") {
		return &Error{Code: CodeHeightUnavailable, Detail: detail}
	}
	return &Error{Code: CodeRPCError, Detail: detail}
}
