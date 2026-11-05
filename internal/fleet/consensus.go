package fleet

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/address"
	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/sanitize"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

// MaxConsensusValidatorsListed bounds the per-validator vote list; beyond
// it only validators that are missing a vote or are in the config are shown.
const MaxConsensusValidatorsListed = 50

type ConsensusReport struct {
	Summary  string             `json:"summary"`
	Level    analysis.Level     `json:"level"`
	Findings []analysis.Finding `json:"findings"`

	SourceNode        string               `json:"source_node"`
	Height            int64                `json:"height"`
	Round             int32                `json:"round"`
	Step              string               `json:"step"`
	SecondsAtHeight   float64              `json:"seconds_at_height"`
	TotalPower        int64                `json:"total_power"`
	Prevotes          VoteTally            `json:"prevotes"`
	Precommits        VoteTally            `json:"precommits"`
	Proposer          string               `json:"proposer,omitempty"`
	Validators        []ConsensusValidator `json:"validators"`
	ValidatorsOmitted int                  `json:"validators_omitted,omitempty"`
	Note              string               `json:"note"`
}

type VoteTally struct {
	ReceivedPct float64 `json:"received_pct"` // power whose vote arrived (block or nil)
	ForNilPct   float64 `json:"for_nil_pct"`
	Missing     int     `json:"missing"`
	Quorum      bool    `json:"quorum"` // received power > 2/3
}

type ConsensusValidator struct {
	Address   string  `json:"address"`
	Node      string  `json:"node,omitempty"`    // name in the config, if it is ours
	Moniker   string  `json:"moniker,omitempty"` // from x/staking, if available
	PowerPct  float64 `json:"power_pct"`
	Prevote   string  `json:"prevote"`   // block | nil | missing | unknown
	Precommit string  `json:"precommit"` // block | nil | missing | unknown
}

const consensusNote = "Votes are as seen by source_node; another node may have received more. " +
	"A validator shows as missing when its vote has not reached this node in the current round."

// Consensus inspects the live consensus round on a node: who has voted in
// the current round and whether a 2/3 quorum is present. Its main use is a
// stalled chain. node may be "" to pick a synced sentry or full node.
func (f *Fleet) Consensus(ctx context.Context, node string) (*ConsensusReport, error) {
	var src source
	if node == "" {
		s, _, err := f.pickSource(ctx, "")
		if err != nil {
			// A halted chain still answers /consensus_state; fall back to any reachable node.
			if s, err = f.anyReachable(ctx); err != nil {
				return nil, err
			}
		}
		src = s
	} else {
		n, err := f.rpcNode(node)
		if err != nil {
			return nil, err
		}
		src = source{n.Name, f.clients[n.Name]}
	}

	rs, err := src.client.ConsensusState(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", src.name, f.nodeError(err).Error())
	}
	set, err := src.client.Validators(ctx, rs.Height)
	if err != nil {
		if set, err = src.client.Validators(ctx, 0); err != nil {
			return nil, fmt.Errorf("%s: validator set: %s", src.name, f.nodeError(err).Error())
		}
	}

	r := &ConsensusReport{
		SourceNode: src.name,
		Height:     rs.Height,
		Round:      rs.Round,
		Step:       comet.StepName(rs.Step),
		Findings:   []analysis.Finding{},
		Validators: []ConsensusValidator{},
		Note:       consensusNote,
	}
	now := f.now()
	r.SecondsAtHeight = max(now.Sub(rs.StartTime), 0).Round(100 * time.Millisecond).Seconds()
	for _, v := range set {
		r.TotalPower += int64(v.VotingPower)
	}

	current := rs.Current()
	names := f.validatorNames()
	monikers := f.monikers(ctx)

	var prevotePower, precommitPower, missingPower int64
	var missing []string
	var all []ConsensusValidator
	mismatched := 0
	tally := func(votes []comet.Vote, i int, addr string, power int64, t *VoteTally, sum *int64) string {
		if i >= len(votes) || !votes[i].Received {
			t.Missing++
			return "missing"
		}
		// Votes are listed by position; make sure this one is from the
		// validator at that position (a set from another height would shift it).
		if !votes[i].MatchesAddress(addr) {
			mismatched++
			return "unknown"
		}
		*sum += power
		if votes[i].ForNil {
			t.ForNilPct += pctOf(power, r.TotalPower)
			return "nil"
		}
		return "block"
	}
	for i, v := range set {
		power := int64(v.VotingPower)
		addr, _ := consensusAddr(v.Address)
		cv := ConsensusValidator{Address: addr, Node: names[addr], Moniker: monikers[addr], PowerPct: roundTo(pctOf(power, r.TotalPower), 2)}
		cv.Prevote = tally(current.Prevotes, i, v.Address, power, &r.Prevotes, &prevotePower)
		cv.Precommit = tally(current.Precommits, i, v.Address, power, &r.Precommits, &precommitPower)
		if cv.Prevote == "missing" {
			missingPower += power
			missing = append(missing, cv.label())
		}
		if v.Address == rs.Proposer.Address {
			r.Proposer = cv.label()
		}
		all = append(all, cv)
	}
	r.Prevotes.ReceivedPct = roundTo(pctOf(prevotePower, r.TotalPower), 2)
	r.Prevotes.ForNilPct = roundTo(r.Prevotes.ForNilPct, 2)
	r.Prevotes.Quorum = analysis.HasQuorum(prevotePower, r.TotalPower)
	r.Precommits.ReceivedPct = roundTo(pctOf(precommitPower, r.TotalPower), 2)
	r.Precommits.ForNilPct = roundTo(r.Precommits.ForNilPct, 2)
	r.Precommits.Quorum = analysis.HasQuorum(precommitPower, r.TotalPower)

	r.Validators, r.ValidatorsOmitted = listConsensusValidators(all)
	if mismatched > 0 {
		r.Findings = append(r.Findings, analysis.Finding{Level: analysis.Unknown, Code: "vote_set_mismatch",
			Message: fmt.Sprintf("%d votes did not match the validator at their position (the validator set read may be from another height); those are shown as unknown and not counted", mismatched)})
	}

	r.Findings = append(r.Findings, analysis.JudgeConsensus(analysis.Consensus{
		Now:             now,
		Height:          rs.Height,
		Round:           rs.Round,
		StartTime:       rs.StartTime,
		TotalPower:      r.TotalPower,
		PrevotePower:    prevotePower,
		PrecommitPower:  precommitPower,
		MissingPrevotes: missing,
		MissingPower:    missingPower,
		StuckAfter:      f.thresholds.StaleWarn,
	})...)
	r.Level = analysis.LevelOf(r.Findings)
	r.Summary = consensusSummary(r)
	return r, nil
}

func (cv ConsensusValidator) label() string {
	switch {
	case cv.Node != "":
		return cv.Node
	case cv.Moniker != "":
		return cv.Moniker
	case hexAddrRe.MatchString(cv.Address):
		return cv.Address[:12] + "…"
	default:
		return cv.Address // already wrapped as untrusted
	}
}

// listConsensusValidators keeps small sets whole; for large ones it keeps
// those missing a vote and our own nodes, so the list stays readable.
func listConsensusValidators(all []ConsensusValidator) ([]ConsensusValidator, int) {
	if len(all) <= MaxConsensusValidatorsListed {
		return all, 0
	}
	var keep []ConsensusValidator
	for _, v := range all {
		if v.Node != "" || v.Prevote == "missing" || v.Precommit == "missing" {
			keep = append(keep, v)
		}
	}
	sort.SliceStable(keep, func(a, b int) bool { return keep[a].PowerPct > keep[b].PowerPct })
	if len(keep) > MaxConsensusValidatorsListed {
		keep = keep[:MaxConsensusValidatorsListed]
	}
	return keep, len(all) - len(keep)
}

// validatorNames maps configured validator addresses to node names.
func (f *Fleet) validatorNames() map[string]string {
	m := map[string]string{}
	for _, n := range f.cfg.Nodes {
		if n.Role == config.RoleValidator && n.ValidatorHex != "" {
			m[n.ValidatorHex] = n.Name
		}
	}
	return m
}

// monikers maps consensus addresses to staking monikers; empty without the
// Cosmos SDK layer or on error.
func (f *Fleet) monikers(ctx context.Context) map[string]string {
	m := map[string]string{}
	if f.cosmos == nil {
		return m
	}
	vals, err := f.cosmos.Validators(ctx)
	if err != nil {
		return m
	}
	for _, v := range vals {
		if a, err := address.ConsensusFromPubKey(v.ConsensusPubkey.Type, v.ConsensusPubkey.Key); err == nil {
			m[a] = sanitize.Untrusted(v.Description.Moniker)
		}
	}
	return m
}

// anyReachable returns the first node that answers /status, synced or not.
func (f *Fleet) anyReachable(ctx context.Context) (source, error) {
	var tried []string
	for _, n := range f.cfg.RPCNodes() {
		if _, err := f.clients[n.Name].Status(ctx); err == nil {
			return source{n.Name, f.clients[n.Name]}, nil
		} else {
			tried = append(tried, n.Name+": "+f.nodeError(err).Code)
		}
	}
	return source{}, fmt.Errorf("no node answered (%s)", strings.Join(tried, "; "))
}

func pctOf(p, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(p) * 100 / float64(total)
}

func consensusSummary(r *ConsensusReport) string {
	s := fmt.Sprintf("height %d round %d (%s) for %.0fs [%s]: prevotes %.1f%%, precommits %.1f%% of power",
		r.Height, r.Round, r.Step, r.SecondsAtHeight, r.Level, r.Prevotes.ReceivedPct, r.Precommits.ReceivedPct)
	if issue := firstMessage(r.Findings, nil); issue != "" {
		s += " — " + issue
	}
	if r.Level == analysis.OK {
		return s // votes still arriving in a healthy round are not news
	}
	var missing []string
	for _, v := range r.Validators {
		if v.Prevote == "missing" {
			missing = append(missing, v.label())
		}
	}
	const maxNamed = 10
	if n := len(missing); n > maxNamed {
		missing = append(missing[:maxNamed], fmt.Sprintf("and %d more", n-maxNamed))
	}
	if len(missing) > 0 {
		s += "; not voting: " + strings.Join(missing, ", ")
	}
	return s
}

func (r *ConsensusReport) GetSummary() string { return r.Summary }
