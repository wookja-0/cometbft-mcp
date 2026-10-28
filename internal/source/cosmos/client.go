// Package cosmos reads chain state from a Cosmos SDK REST (gRPC-gateway)
// endpoint: slashing, staking, upgrade and governance queries. Like the
// comet package it decodes only the fields cometbft-mcp uses.
// Supported: Cosmos SDK v0.47 – v0.54.
package cosmos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/source/jsonnum"
)

const (
	CodeBadResponse = "rest_bad_response"
	CodeNotFound    = "rest_not_found"
	CodeRESTError   = "rest_error"
)

type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

type Int64 = jsonnum.Int64

type NodeInfo struct {
	DefaultNodeInfo struct {
		Network string `json:"network"`
	} `json:"default_node_info"`
	ApplicationVersion struct {
		AppName          string `json:"app_name"`
		Version          string `json:"version"`
		CosmosSDKVersion string `json:"cosmos_sdk_version"`
	} `json:"application_version"`
}

type SlashingParams struct {
	SignedBlocksWindow    Int64  `json:"signed_blocks_window"`
	MinSignedPerWindow    string `json:"min_signed_per_window"` // decimal, e.g. "0.050000000000000000"
	DowntimeJailDuration  string `json:"downtime_jail_duration"`
	SlashFractionDowntime string `json:"slash_fraction_downtime"`
}

type SigningInfo struct {
	Address             string    `json:"address"`
	StartHeight         Int64     `json:"start_height"`
	IndexOffset         Int64     `json:"index_offset"`
	JailedUntil         time.Time `json:"jailed_until"`
	Tombstoned          bool      `json:"tombstoned"`
	MissedBlocksCounter Int64     `json:"missed_blocks_counter"`
}

type Validator struct {
	OperatorAddress string `json:"operator_address"`
	ConsensusPubkey struct {
		Type string `json:"@type"`
		Key  string `json:"key"`
	} `json:"consensus_pubkey"`
	Jailed      bool   `json:"jailed"`
	Status      string `json:"status"` // BOND_STATUS_BONDED | _UNBONDING | _UNBONDED
	Tokens      string `json:"tokens"`
	Description struct {
		Moniker string `json:"moniker"`
	} `json:"description"`
}

type Plan struct {
	Name   string `json:"name"`
	Height Int64  `json:"height"`
	Info   string `json:"info"`
}

type Proposal struct {
	ID            Int64     `json:"id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	VotingEndTime time.Time `json:"voting_end_time"`
	Messages      []struct {
		Type string `json:"@type"`
		Plan *Plan  `json:"plan"`
	} `json:"messages"`
}

// UpgradePlan returns the software-upgrade plan a proposal carries, if any.
func (p Proposal) UpgradePlan() *Plan {
	for _, m := range p.Messages {
		if m.Type == "/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade" && m.Plan != nil {
			return m.Plan
		}
	}
	return nil
}

type Client struct {
	http *safehttp.Client
	base *url.URL
}

func NewClient(h *safehttp.Client, base *url.URL) *Client {
	return &Client{http: h, base: base}
}

func (c *Client) NodeInfo(ctx context.Context) (*NodeInfo, error) {
	var out NodeInfo
	return &out, c.get(ctx, safehttp.CosmosNodeInfo, "", nil, &out)
}

func (c *Client) Bech32Prefix(ctx context.Context) (string, error) {
	var out struct {
		Bech32Prefix string `json:"bech32_prefix"`
	}
	if err := c.get(ctx, safehttp.CosmosBech32Prefix, "", nil, &out); err != nil {
		return "", err
	}
	if out.Bech32Prefix == "" {
		return "", &Error{Code: CodeBadResponse, Detail: "empty bech32 prefix"}
	}
	return out.Bech32Prefix, nil
}

func (c *Client) SlashingParams(ctx context.Context) (*SlashingParams, error) {
	var out struct {
		Params SlashingParams `json:"params"`
	}
	if err := c.get(ctx, safehttp.CosmosSlashingParams, "", nil, &out); err != nil {
		return nil, err
	}
	return &out.Params, nil
}

func (c *Client) SigningInfo(ctx context.Context, valcons string) (*SigningInfo, error) {
	var out struct {
		ValSigningInfo SigningInfo `json:"val_signing_info"`
	}
	if err := c.get(ctx, safehttp.CosmosSigningInfo, valcons, nil, &out); err != nil {
		return nil, err
	}
	return &out.ValSigningInfo, nil
}

const (
	validatorsPageSize = 200
	maxValidatorPages  = 10
)

// Validators lists every staking validator (all statuses).
func (c *Client) Validators(ctx context.Context) ([]Validator, error) {
	var all []Validator
	key := ""
	for range maxValidatorPages {
		q := url.Values{"pagination.limit": {strconv.Itoa(validatorsPageSize)}}
		if key != "" {
			q.Set("pagination.key", key)
		}
		var out struct {
			Validators []Validator `json:"validators"`
			Pagination struct {
				NextKey *string `json:"next_key"`
			} `json:"pagination"`
		}
		if err := c.get(ctx, safehttp.CosmosValidators, "", q, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Validators...)
		if out.Pagination.NextKey == nil || *out.Pagination.NextKey == "" {
			return all, nil
		}
		key = *out.Pagination.NextKey
	}
	return nil, &Error{Code: CodeBadResponse, Detail: fmt.Sprintf("more than %d staking validators", validatorsPageSize*maxValidatorPages)}
}

// CurrentPlan returns the scheduled upgrade, or nil when none is scheduled.
func (c *Client) CurrentPlan(ctx context.Context) (*Plan, error) {
	var out struct {
		Plan *Plan `json:"plan"`
	}
	if err := c.get(ctx, safehttp.CosmosUpgradePlan, "", nil, &out); err != nil {
		return nil, err
	}
	return out.Plan, nil
}

// VotingProposals lists proposals currently in their voting period.
func (c *Client) VotingProposals(ctx context.Context) ([]Proposal, error) {
	q := url.Values{"proposal_status": {"PROPOSAL_STATUS_VOTING_PERIOD"}, "pagination.limit": {"50"}}
	var out struct {
		Proposals []Proposal `json:"proposals"`
	}
	if err := c.get(ctx, safehttp.CosmosProposals, "", q, &out); err != nil {
		return nil, err
	}
	return out.Proposals, nil
}

func (c *Client) get(ctx context.Context, e safehttp.Endpoint, segment string, q url.Values, out any) error {
	body, err := c.http.GetSegment(ctx, c.base, e, segment, q)
	if err != nil {
		return err
	}
	// gRPC-gateway errors: {"code": 5, "message": "...", "details": []}
	var gwErr struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &gwErr) == nil && gwErr.Code != nil && *gwErr.Code != 0 {
		detail := gwErr.Message
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
		if *gwErr.Code == 5 { // gRPC NotFound
			return &Error{Code: CodeNotFound, Detail: detail}
		}
		return &Error{Code: CodeRESTError, Detail: detail}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &Error{Code: CodeBadResponse, Detail: e.Path() + " did not return the expected JSON (is this a Cosmos SDK REST URL?)"}
	}
	return nil
}
