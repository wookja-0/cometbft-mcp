package fleet

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/address"
	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/sanitize"
	"github.com/wookja-0/cometbft-mcp/internal/source/cosmos"
)

// CosmosValidator is the Cosmos SDK view of a validator: staking status and
// the x/slashing downtime counter.
type CosmosValidator struct {
	Error *NodeError `json:"error,omitempty"`

	ConsensusAddress     string     `json:"consensus_address,omitempty"`
	OperatorAddress      string     `json:"operator_address,omitempty"`
	Moniker              string     `json:"moniker,omitempty"`
	Status               string     `json:"status,omitempty"`
	Jailed               bool       `json:"jailed"`
	JailedUntil          *time.Time `json:"jailed_until,omitempty"`
	Tombstoned           bool       `json:"tombstoned"`
	SigningWindow        int64      `json:"signing_window,omitempty"`
	MinSignedPerWindow   string     `json:"min_signed_per_window,omitempty"`
	MaxMissed            int64      `json:"max_missed,omitempty"`
	MissedInWindow       int64      `json:"missed_in_window"`
	MissesUntilJail      *int64     `json:"misses_until_jail,omitempty"`
	GraceUntilHeight     int64      `json:"grace_until_height,omitempty"`
	TimeToJail           string     `json:"time_to_jail,omitempty"`
	DowntimeJailDuration string     `json:"downtime_jail_duration,omitempty"`
}

const CodeWrongChain = "rest_wrong_chain"

// cosmosValidator gathers the SDK view of the validator with consensus
// address hexAddr. Failures are reported inside the result, not as an error,
// so the liveness part of the report still stands.
func (f *Fleet) cosmosValidator(ctx context.Context, hexAddr, chainID string, height int64, streak int, blockTime time.Duration) (*CosmosValidator, []analysis.Finding) {
	cv := &CosmosValidator{}
	fail := func(err error) (*CosmosValidator, []analysis.Finding) {
		cv.Error = f.nodeError(err)
		return cv, []analysis.Finding{{Level: analysis.Unknown, Code: "cosmos_unavailable", Message: "Cosmos SDK data unavailable: " + cv.Error.Error()}}
	}

	if err := f.checkRESTChain(ctx, chainID); err != nil {
		return fail(err)
	}
	prefix, err := f.cosmos.Bech32Prefix(ctx)
	if err != nil {
		return fail(err)
	}
	raw, err := hex.DecodeString(hexAddr)
	if err != nil {
		return fail(&cosmos.Error{Code: cosmos.CodeBadResponse, Detail: "validator address is not hex"})
	}
	if cv.ConsensusAddress, err = address.EncodeBech32(prefix+"valcons", raw); err != nil {
		return fail(err)
	}

	params, err := f.cosmos.SlashingParams(ctx)
	if err != nil {
		return fail(err)
	}
	cv.SigningWindow = int64(params.SignedBlocksWindow)
	cv.MinSignedPerWindow = params.MinSignedPerWindow
	cv.DowntimeJailDuration = params.DowntimeJailDuration
	if _, err := time.ParseDuration(cv.DowntimeJailDuration); err != nil {
		cv.DowntimeJailDuration = sanitize.Untrusted(cv.DowntimeJailDuration)
	}
	if cv.MaxMissed, err = analysis.MaxMissed(cv.SigningWindow, params.MinSignedPerWindow); err != nil {
		return fail(&cosmos.Error{Code: cosmos.CodeBadResponse, Detail: err.Error()})
	}

	// Staking status is best-effort: without it the slashing judgment still holds.
	if vals, err := f.cosmos.Validators(ctx); err == nil {
		for _, v := range vals {
			if a, err := address.ConsensusFromPubKey(v.ConsensusPubkey.Type, v.ConsensusPubkey.Key); err == nil && a == hexAddr {
				cv.OperatorAddress = bech32Addr(v.OperatorAddress)
				cv.Moniker = sanitize.Untrusted(v.Description.Moniker)
				cv.Status = bondStatus(v.Status)
				cv.Jailed = v.Jailed
				break
			}
		}
	}

	info, err := f.cosmos.SigningInfo(ctx, cv.ConsensusAddress)
	if err != nil {
		if ne := f.nodeError(err); ne.Code == cosmos.CodeNotFound {
			return cv, []analysis.Finding{{Level: analysis.Warn, Code: "no_signing_info", Message: "the chain has no signing info for this consensus address: the validator never bonded, or validator_address is wrong"}}
		}
		return fail(err)
	}
	cv.Tombstoned = info.Tombstoned
	cv.MissedInWindow = int64(info.MissedBlocksCounter)
	// A future jailed_until means jailed even if the staking lookup failed;
	// relying on the counter alone would look healthy, since jailing resets it.
	if info.JailedUntil.After(f.now()) {
		cv.Jailed = true
	}
	if !info.JailedUntil.IsZero() && info.JailedUntil.Unix() > 0 {
		t := info.JailedUntil
		cv.JailedUntil = &t
	}

	view, fs := analysis.JudgeSlashing(analysis.Slashing{
		Now:         f.now(),
		Height:      height,
		Window:      cv.SigningWindow,
		MaxMissed:   cv.MaxMissed,
		Missed:      cv.MissedInWindow,
		StartHeight: int64(info.StartHeight),
		Jailed:      cv.Jailed,
		JailedUntil: info.JailedUntil,
		Tombstoned:  info.Tombstoned,
		Status:      cv.Status,
		MissStreak:  streak,
		BlockTime:   blockTime,
	})
	if !cv.Jailed && !cv.Tombstoned {
		cv.MissesUntilJail = &view.MissesUntilJail
		cv.GraceUntilHeight = view.GraceUntilHeight
		if view.TimeToJail > 0 {
			cv.TimeToJail = view.TimeToJail.Round(time.Second).String()
		}
	}
	return cv, fs
}

// bondStatus passes x/staking's enum through and wraps anything else.
func bondStatus(s string) string {
	switch s {
	case "BOND_STATUS_BONDED", "BOND_STATUS_UNBONDING", "BOND_STATUS_UNBONDED", "BOND_STATUS_UNSPECIFIED":
		return s
	}
	return sanitize.Untrusted(s)
}

// checkRESTChain makes sure cosmos.rest serves the same chain as the nodes.
func (f *Fleet) checkRESTChain(ctx context.Context, chainIDWant string) error {
	ni, err := f.cosmos.NodeInfo(ctx)
	if err != nil {
		return err
	}
	if got := ni.DefaultNodeInfo.Network; chainIDWant != "" && got != chainIDWant {
		return &cosmos.Error{Code: CodeWrongChain, Detail: fmt.Sprintf("cosmos.rest is on chain %s but the nodes are on %s", chainID(got), chainID(chainIDWant))}
	}
	return nil
}
