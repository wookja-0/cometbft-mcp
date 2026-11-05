package fleet

import (
	"context"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/sanitize"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

type StatusReport struct {
	Summary string         `json:"summary"`
	Level   analysis.Level `json:"level"`
	Nodes   []NodeStatus   `json:"nodes"`
}

type NodeStatus struct {
	Name     string             `json:"name"`
	Role     config.Role        `json:"role"`
	Level    analysis.Level     `json:"level"`
	Findings []analysis.Finding `json:"findings"`
	Error    *NodeError         `json:"error,omitempty"`

	ChainID           string     `json:"chain_id,omitempty"`
	Moniker           string     `json:"moniker,omitempty"`
	CometBFTVersion   string     `json:"cometbft_version,omitempty"`
	App               string     `json:"app,omitempty"`
	AppVersion        string     `json:"app_version,omitempty"`
	LatestHeight      int64      `json:"latest_height,omitempty"`
	LatestBlockTime   *time.Time `json:"latest_block_time,omitempty"`
	SecondsSinceBlock float64    `json:"seconds_since_block,omitempty"`
	CatchingUp        bool       `json:"catching_up"`
	EarliestHeight    int64      `json:"earliest_height,omitempty"`
	BehindFleetBlocks *int64     `json:"behind_fleet_blocks,omitempty"`
	ValidatorAddress  string     `json:"validator_address,omitempty"`
	VotingPower       *int64     `json:"voting_power,omitempty"`
}

type rawStatus struct {
	status *comet.Status
	abci   *comet.ABCIInfo
	err    error
}

// Status reports sync health for one node, or every node when name is "".
func (f *Fleet) Status(ctx context.Context, name string) (*StatusReport, error) {
	nodes, err := f.selectNodes(name)
	if err != nil {
		return nil, err
	}
	// Lag is judged against the whole fleet even when one node is asked for.
	fleetNodes := f.cfg.RPCNodes()
	all := each(fleetNodes, func(n config.Node) rawStatus {
		c := f.clients[n.Name]
		s, err := c.Status(ctx)
		if err != nil {
			return rawStatus{err: err}
		}
		a, _ := c.ABCIInfo(ctx) // optional; some providers block it
		return rawStatus{status: s, abci: a}
	})
	byName := map[string]rawStatus{}
	for i, n := range fleetNodes {
		byName[n.Name] = all[i]
	}

	ref := referenceHeights(fleetNodes, all)
	now := f.now()
	var report StatusReport
	var names []string
	var levels []analysis.Level
	var issues []string
	for _, n := range nodes {
		ns := f.nodeStatus(n, byName[n.Name], ref, now)
		report.Nodes = append(report.Nodes, ns)
		names = append(names, n.Name)
		levels = append(levels, ns.Level)
		issues = append(issues, firstMessage(ns.Findings, ns.Error))
	}
	report.Level = analysis.Worst(levels...)
	report.Summary = summarize("nodes", names, levels, issues)
	return &report, nil
}

// referenceHeights maps chain ID to the highest height any reachable node
// reports. Lag is only meaningful when two or more nodes share a chain.
func referenceHeights(nodes []config.Node, raws []rawStatus) map[string]int64 {
	max := map[string]int64{}
	count := map[string]int{}
	for _, r := range raws {
		if r.status == nil {
			continue
		}
		id := r.status.NodeInfo.Network
		count[id]++
		if h := int64(r.status.SyncInfo.LatestBlockHeight); h > max[id] {
			max[id] = h
		}
	}
	for id, c := range count {
		if c < 2 {
			delete(max, id)
		}
	}
	return max
}

func (f *Fleet) nodeStatus(n config.Node, r rawStatus, ref map[string]int64, now time.Time) NodeStatus {
	ns := NodeStatus{Name: n.Name, Role: n.Role, Findings: []analysis.Finding{}}
	if r.err != nil {
		ns.Error = f.nodeError(r.err)
		ns.Level = analysis.Critical
		ns.Findings = append(ns.Findings, analysis.Finding{Level: analysis.Critical, Code: "unreachable", Message: "RPC did not answer: " + ns.Error.Error()})
		return ns
	}
	s := r.status
	ns.ChainID = chainID(s.NodeInfo.Network)
	ns.Moniker = sanitize.Untrusted(s.NodeInfo.Moniker)
	ns.CometBFTVersion = sanitize.Untrusted(s.NodeInfo.Version)
	if r.abci != nil {
		if r.abci.Response.Data != "" {
			ns.App = sanitize.Untrusted(r.abci.Response.Data)
		}
		if r.abci.Response.Version != "" {
			ns.AppVersion = sanitize.Untrusted(r.abci.Response.Version)
		}
	}
	ns.LatestHeight = int64(s.SyncInfo.LatestBlockHeight)
	t := s.SyncInfo.LatestBlockTime
	ns.LatestBlockTime = &t
	ns.SecondsSinceBlock = now.Sub(s.SyncInfo.LatestBlockTime).Round(100 * time.Millisecond).Seconds()
	ns.CatchingUp = s.SyncInfo.CatchingUp
	ns.EarliestHeight = int64(s.SyncInfo.EarliestBlockHeight)

	refHeight := ref[s.NodeInfo.Network]
	if refHeight > 0 {
		lag := refHeight - ns.LatestHeight
		ns.BehindFleetBlocks = &lag
	}
	ns.Findings = append(ns.Findings, analysis.JudgeSync(analysis.Sync{
		CatchingUp:      s.SyncInfo.CatchingUp,
		LatestHeight:    ns.LatestHeight,
		LatestTime:      s.SyncInfo.LatestBlockTime,
		ReferenceHeight: refHeight,
	}, now, f.thresholds)...)

	if n.Role == config.RoleValidator {
		ns.ValidatorAddress, _ = consensusAddr(s.ValidatorInfo.Address)
		vp := int64(s.ValidatorInfo.VotingPower)
		ns.VotingPower = &vp
		if n.ValidatorHex != "" && n.ValidatorHex != ns.ValidatorAddress {
			ns.Findings = append(ns.Findings, analysis.Finding{Level: analysis.Warn, Code: "address_mismatch", Message: "the node signs with a different consensus key than validator_address in the config"})
		}
		if vp == 0 {
			ns.Findings = append(ns.Findings, analysis.Finding{Level: analysis.Warn, Code: "no_voting_power", Message: "role is validator but the node's key has no voting power (not in the active set, jailed, or the wrong key is loaded)"})
		}
	}
	ns.Level = analysis.LevelOf(ns.Findings)
	return ns
}
