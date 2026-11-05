package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/sanitize"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

// ErrNoCosmos is returned by tools that need the Cosmos SDK layer.
var ErrNoCosmos = errors.New("this needs the Cosmos SDK layer: add `cosmos: {rest: <REST URL of a sentry or full node>}` to the config")

// blockTimeSample is how many blocks back the average block time is taken over.
const blockTimeSample = 100

type UpgradeReport struct {
	Summary  string             `json:"summary"`
	Level    analysis.Level     `json:"level"`
	Findings []analysis.Finding `json:"findings"`

	ChainID         string         `json:"chain_id"`
	SourceNode      string         `json:"source_node"`
	Height          int64          `json:"height"`
	AvgBlockSeconds float64        `json:"avg_block_seconds,omitempty"`
	Plan            *PlanView      `json:"plan,omitempty"`
	Proposals       []ProposalView `json:"upgrade_proposals_in_voting"`
	Nodes           []NodeVersion  `json:"nodes"`
	Note            string         `json:"note"`
}

type PlanView struct {
	Name            string     `json:"name"`
	Height          int64      `json:"height"`
	BlocksRemaining int64      `json:"blocks_remaining"`
	ETA             *time.Time `json:"eta,omitempty"`
	ETAIn           string     `json:"eta_in,omitempty"`
	Info            string     `json:"info,omitempty"`
}

type ProposalView struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	UpgradeName   string    `json:"upgrade_name"`
	UpgradeHeight int64     `json:"upgrade_height"`
	VotingEndTime time.Time `json:"voting_end_time"`
}

type NodeVersion struct {
	Name            string     `json:"name"`
	AppVersion      string     `json:"app_version,omitempty"`
	CometBFTVersion string     `json:"cometbft_version,omitempty"`
	Height          int64      `json:"height,omitempty"`
	Error           *NodeError `json:"error,omitempty"`
}

const upgradeNote = "cometbft-mcp cannot see whether the new binary is staged on each host (it has no filesystem access by design). " +
	"Confirm that yourself, e.g. cosmovisor's upgrades/<name>/bin, before the upgrade height."

// Upgrade reports the scheduled software upgrade, upgrade proposals in
// voting, and the app version each node runs.
func (f *Fleet) Upgrade(ctx context.Context) (*UpgradeReport, error) {
	if f.cosmos == nil {
		return nil, ErrNoCosmos
	}
	src, st, err := f.pickSource(ctx, "")
	if err != nil {
		return nil, err
	}
	r := &UpgradeReport{
		ChainID:    chainID(st.NodeInfo.Network),
		SourceNode: src.name,
		Height:     int64(st.SyncInfo.LatestBlockHeight),
		Findings:   []analysis.Finding{},
		Proposals:  []ProposalView{},
		Note:       upgradeNote,
	}
	if err := f.checkRESTChain(ctx, st.NodeInfo.Network); err != nil {
		return nil, fmt.Errorf("%s", f.nodeError(err).Error())
	}

	blockTime := f.blockTime(ctx, src.client, r.Height, int64(st.SyncInfo.EarliestBlockHeight))
	if blockTime > 0 {
		r.AvgBlockSeconds = roundTo(blockTime.Seconds(), 2)
	}

	plan, err := f.cosmos.CurrentPlan(ctx)
	if err != nil {
		r.Findings = append(r.Findings, analysis.Finding{Level: analysis.Unknown, Code: "plan_unavailable", Message: "could not read the upgrade plan: " + f.nodeError(err).Error()})
	} else if plan != nil && plan.Name != "" {
		name := sanitize.Untrusted(plan.Name)
		eta, fs := analysis.JudgeUpgrade(analysis.Upgrade{
			Now:        f.now(),
			Height:     r.Height,
			HeadTime:   st.SyncInfo.LatestBlockTime,
			BlockTime:  blockTime,
			PlanName:   name,
			PlanHeight: int64(plan.Height),
			StaleAfter: f.thresholds.StaleWarn,
		})
		r.Findings = append(r.Findings, fs...)
		r.Plan = &PlanView{Name: name, Height: int64(plan.Height), BlocksRemaining: max(int64(plan.Height)-r.Height, 0)}
		if plan.Info != "" {
			r.Plan.Info = sanitize.Untrusted(plan.Info)
		}
		if !eta.IsZero() {
			r.Plan.ETA = &eta
			r.Plan.ETAIn = durationString(eta.Sub(f.now()))
		}
	}

	props, err := f.cosmos.VotingProposals(ctx)
	if err != nil {
		r.Findings = append(r.Findings, analysis.Finding{Level: analysis.Unknown, Code: "proposals_unavailable", Message: "could not read governance proposals: " + f.nodeError(err).Error()})
	}
	for _, p := range props {
		if up := p.UpgradePlan(); up != nil {
			r.Proposals = append(r.Proposals, ProposalView{
				ID:            int64(p.ID),
				Title:         sanitize.Untrusted(p.Title),
				UpgradeName:   sanitize.Untrusted(up.Name),
				UpgradeHeight: int64(up.Height),
				VotingEndTime: p.VotingEndTime,
			})
		}
	}

	versions := map[string]string{}
	r.Nodes = each(f.cfg.RPCNodes(), func(n config.Node) NodeVersion {
		nv := NodeVersion{Name: n.Name}
		c := f.clients[n.Name]
		s, err := c.Status(ctx)
		if err != nil {
			nv.Error = f.nodeError(err)
			return nv
		}
		nv.Height = int64(s.SyncInfo.LatestBlockHeight)
		nv.CometBFTVersion = sanitize.Untrusted(s.NodeInfo.Version)
		if a, err := c.ABCIInfo(ctx); err == nil && a.Response.Version != "" {
			nv.AppVersion = sanitize.Untrusted(a.Response.Version)
		}
		return nv
	})
	for _, nv := range r.Nodes {
		versions[nv.Name] = nv.AppVersion
	}
	r.Findings = append(r.Findings, analysis.JudgeVersions(versions)...)

	r.Level = analysis.LevelOf(r.Findings)
	r.Summary = upgradeSummary(r)
	return r, nil
}

// blockTime averages the block interval over the last blockTimeSample
// canonical blocks; 0 if it cannot be measured.
func (f *Fleet) blockTime(ctx context.Context, c *comet.Client, latest, earliest int64) time.Duration {
	to := latest - 1
	from := max(to-blockTimeSample, earliest, 1)
	if to <= from {
		return 0
	}
	a, err := c.Commit(ctx, from)
	if err != nil {
		return 0
	}
	b, err := c.Commit(ctx, to)
	if err != nil {
		return 0
	}
	span := b.Header.Time.Sub(a.Header.Time)
	if span <= 0 {
		return 0
	}
	return span / time.Duration(to-from)
}

func upgradeSummary(r *UpgradeReport) string {
	var parts []string
	if r.Plan == nil {
		parts = append(parts, "no upgrade scheduled")
	} else {
		s := fmt.Sprintf("upgrade %s at height %d", r.Plan.Name, r.Plan.Height)
		switch {
		case r.Plan.BlocksRemaining == 0:
			s += " (height reached)"
		case r.Plan.ETAIn != "":
			s += fmt.Sprintf(" in ~%s (%d blocks)", r.Plan.ETAIn, r.Plan.BlocksRemaining)
		default:
			s += fmt.Sprintf(" (%d blocks away)", r.Plan.BlocksRemaining)
		}
		parts = append(parts, s)
	}
	if n := len(r.Proposals); n > 0 {
		parts = append(parts, fmt.Sprintf("%d upgrade proposal(s) in voting", n))
	}
	s := fmt.Sprintf("%s [%s]: %s", r.ChainID, r.Level, strings.Join(parts, "; "))
	if issue := firstMessage(r.Findings, nil); issue != "" && r.Level != analysis.OK {
		s += " — " + issue
	}
	return s
}

func (r *UpgradeReport) GetSummary() string { return r.Summary }
