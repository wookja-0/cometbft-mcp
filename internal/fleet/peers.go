package fleet

import (
	"context"
	"sort"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/sanitize"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

// MaxPeersListed bounds the per-node peer list in a report.
const MaxPeersListed = 40

type PeerReport struct {
	Summary string         `json:"summary"`
	Level   analysis.Level `json:"level"`
	Masked  bool           `json:"network_details_masked"`
	Nodes   []NodePeers    `json:"nodes"`
}

type NodePeers struct {
	Name     string             `json:"name"`
	Role     config.Role        `json:"role"`
	Level    analysis.Level     `json:"level"`
	Findings []analysis.Finding `json:"findings"`
	Error    *NodeError         `json:"error,omitempty"`

	Listening    bool       `json:"listening"`
	Listeners    []string   `json:"listeners,omitempty"`
	Total        int        `json:"total"`
	Inbound      int        `json:"inbound"`
	Outbound     int        `json:"outbound"`
	PublicPeers  int        `json:"public_peers"`
	Peers        []PeerView `json:"peers,omitempty"`
	PeersOmitted int        `json:"peers_omitted,omitempty"`
}

type PeerView struct {
	NodeID       string `json:"node_id"`
	Moniker      string `json:"moniker"`
	Address      string `json:"address"` // address class unless revealing is on
	Direction    string `json:"direction"`
	ConnectedFor string `json:"connected_for,omitempty"`
	OtherChain   bool   `json:"other_chain,omitempty"` // peer reports a different chain ID
}

// Peers reports p2p connectivity for one node, or every node when name is "".
func (f *Fleet) Peers(ctx context.Context, name string) (*PeerReport, error) {
	nodes, err := f.selectNodes(name)
	if err != nil {
		return nil, err
	}
	type raw struct {
		net     *comet.NetInfo
		chainID string
		err     error
	}
	raws := each(nodes, func(n config.Node) raw {
		c := f.clients[n.Name]
		ni, err := c.NetInfo(ctx)
		if err != nil {
			return raw{err: err}
		}
		r := raw{net: ni}
		if s, err := c.Status(ctx); err == nil {
			r.chainID = s.NodeInfo.Network
		}
		return r
	})

	report := PeerReport{Masked: !f.net.Reveal}
	var names []string
	var levels []analysis.Level
	var issues []string
	for i, n := range nodes {
		np := NodePeers{Name: n.Name, Role: n.Role, Findings: []analysis.Finding{}}
		if r := raws[i]; r.err != nil {
			np.Error = f.nodeError(r.err)
			np.Level = analysis.Critical
			np.Findings = append(np.Findings, analysis.Finding{Level: analysis.Critical, Code: "unreachable", Message: "RPC did not answer: " + np.Error.Error()})
		} else {
			f.fillPeers(&np, n, r.net, r.chainID)
		}
		report.Nodes = append(report.Nodes, np)
		names = append(names, n.Name)
		levels = append(levels, np.Level)
		issues = append(issues, firstMessage(np.Findings, np.Error))
	}
	report.Level = analysis.Worst(levels...)
	report.Summary = summarize("nodes", names, levels, issues)
	return &report, nil
}

func (f *Fleet) fillPeers(np *NodePeers, n config.Node, ni *comet.NetInfo, chainID string) {
	np.Listening = ni.Listening
	for _, l := range ni.Listeners {
		// CometBFT v0.38+ reports "Listener(@)" with no address.
		if a := listenerAddr(l); a != "" {
			np.Listeners = append(np.Listeners, f.net.ListenAddr(a))
		}
	}
	np.Total = len(ni.Peers)

	peers := append([]comet.Peer(nil), ni.Peers...)
	// Longest-lived first: the stable core of the node's connectivity.
	sort.SliceStable(peers, func(a, b int) bool {
		return peers[a].ConnectionStatus.Duration > peers[b].ConnectionStatus.Duration
	})
	otherChain := 0
	for i, p := range peers {
		if p.IsOutbound {
			np.Outbound++
		} else {
			np.Inbound++
		}
		if sanitize.IsPublic(p.RemoteIP) {
			np.PublicPeers++
		}
		other := chainID != "" && p.NodeInfo.Network != chainID
		if other {
			otherChain++
		}
		if i >= MaxPeersListed {
			np.PeersOmitted++
			continue
		}
		dir := "inbound"
		if p.IsOutbound {
			dir = "outbound"
		}
		pv := PeerView{
			NodeID:     f.net.NodeID(p.NodeInfo.ID),
			Moniker:    sanitize.Untrusted(p.NodeInfo.Moniker),
			Address:    f.net.IP(p.RemoteIP),
			Direction:  dir,
			OtherChain: other,
		}
		if d := time.Duration(p.ConnectionStatus.Duration); d > 0 {
			pv.ConnectedFor = durationString(d)
		}
		np.Peers = append(np.Peers, pv)
	}

	np.Findings = append(np.Findings, analysis.JudgePeers(analysis.Peers{
		Validator:   n.Role == config.RoleValidator,
		Listening:   ni.Listening,
		Total:       np.Total,
		Inbound:     np.Inbound,
		Outbound:    np.Outbound,
		PublicPeers: np.PublicPeers,
	})...)
	if otherChain > 0 {
		np.Findings = append(np.Findings, analysis.Finding{Level: analysis.Warn, Code: "peers_on_other_chain", Message: "some peers report a different chain ID; check persistent_peers and seeds"})
	}
	np.Level = analysis.LevelOf(np.Findings)
}

// listenerAddr extracts the address from a net_info listener string such as
// "Listener(@tcp://0.0.0.0:26656)" (CometBFT's format) or a bare address.
func listenerAddr(s string) string {
	if i := len("Listener(@"); len(s) > i && s[:i] == "Listener(@" && s[len(s)-1] == ')' {
		return s[i : len(s)-1]
	}
	return s
}
