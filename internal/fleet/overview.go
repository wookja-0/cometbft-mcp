package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
)

// OverviewWindow is the liveness window fleet_overview uses per validator;
// smaller than validator_health's default to keep a full sweep quick.
const OverviewWindow = 50

type OverviewReport struct {
	Summary    string              `json:"summary"`
	Level      analysis.Level      `json:"level"`
	Nodes      []OverviewNode      `json:"nodes"`
	Validators []OverviewValidator `json:"validators"`
	Consensus  *OverviewItem       `json:"consensus,omitempty"`
	Upgrade    *OverviewItem       `json:"upgrade,omitempty"`
}

type OverviewNode struct {
	Name   string         `json:"name"`
	Role   config.Role    `json:"role"`
	Level  analysis.Level `json:"level"`
	Height int64          `json:"height,omitempty"`
	Issues []string       `json:"issues"`
}

type OverviewValidator struct {
	Name    string         `json:"name"`
	Level   analysis.Level `json:"level"`
	Summary string         `json:"summary"`
}

type OverviewItem struct {
	Level   analysis.Level `json:"level"`
	Summary string         `json:"summary"`
}

// Overview sweeps every node and validator in one call: sync, peers,
// signing liveness, and (with the Cosmos SDK layer) jail risk and upgrades.
func (f *Fleet) Overview(ctx context.Context) (*OverviewReport, error) {
	st, err := f.Status(ctx, "")
	if err != nil {
		return nil, err
	}
	pe, err := f.Peers(ctx, "")
	if err != nil {
		return nil, err
	}

	r := &OverviewReport{}
	levels := []analysis.Level{}
	for i, ns := range st.Nodes {
		np := pe.Nodes[i]
		on := OverviewNode{Name: ns.Name, Role: ns.Role, Height: ns.LatestHeight, Issues: []string{}}
		on.Level = analysis.Worst(ns.Level, np.Level)
		for _, fs := range [][]analysis.Finding{ns.Findings, np.Findings} {
			for _, x := range fs {
				on.Issues = append(on.Issues, fmt.Sprintf("[%s] %s: %s", x.Level, x.Code, x.Message))
			}
		}
		// An unreachable node shows up in both reports; say it once.
		if ns.Error != nil {
			on.Issues = on.Issues[:1]
		}
		r.Nodes = append(r.Nodes, on)
		levels = append(levels, on.Level)
	}

	var vals []config.Node
	for _, n := range f.cfg.Nodes {
		if n.Role == config.RoleValidator {
			vals = append(vals, n)
		}
	}
	r.Validators = each(vals, func(n config.Node) OverviewValidator {
		vr, err := f.Validator(ctx, n.Name, OverviewWindow)
		if err != nil {
			return OverviewValidator{Name: n.Name, Level: analysis.Unknown, Summary: err.Error()}
		}
		return OverviewValidator{Name: n.Name, Level: vr.Level, Summary: vr.Summary}
	})
	if r.Validators == nil {
		r.Validators = []OverviewValidator{}
	}
	for _, v := range r.Validators {
		levels = append(levels, v.Level)
	}

	if cr, err := f.Consensus(ctx, ""); err == nil {
		r.Consensus = &OverviewItem{Level: cr.Level, Summary: cr.Summary}
		levels = append(levels, cr.Level)
	}

	if f.cosmos != nil {
		if up, err := f.Upgrade(ctx); err == nil {
			r.Upgrade = &OverviewItem{Level: up.Level, Summary: up.Summary}
		} else if !errors.Is(err, ErrNoCosmos) {
			r.Upgrade = &OverviewItem{Level: analysis.Unknown, Summary: err.Error()}
		}
		if r.Upgrade != nil {
			levels = append(levels, r.Upgrade.Level)
		}
	}

	r.Level = analysis.Worst(levels...)
	r.Summary = overviewSummary(r)
	return r, nil
}

func overviewSummary(r *OverviewReport) string {
	count := func(n int, kind string, levels []analysis.Level) string {
		c := map[analysis.Level]int{}
		for _, l := range levels {
			c[l]++
		}
		var parts []string
		for _, l := range []analysis.Level{analysis.OK, analysis.Unknown, analysis.Warn, analysis.Critical} {
			if c[l] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", c[l], l))
			}
		}
		if n == 1 {
			kind = strings.TrimSuffix(kind, "s")
		}
		return fmt.Sprintf("%d %s (%s)", n, kind, strings.Join(parts, ", "))
	}
	var nl, vl []analysis.Level
	var attention []string
	for _, n := range r.Nodes {
		nl = append(nl, n.Level)
		if n.Level == analysis.Critical || n.Level == analysis.Warn {
			attention = append(attention, n.Name)
		}
	}
	for _, v := range r.Validators {
		vl = append(vl, v.Level)
		if (v.Level == analysis.Critical || v.Level == analysis.Warn) && !contains(attention, v.Name) {
			attention = append(attention, v.Name)
		}
	}
	s := fmt.Sprintf("[%s] %s", r.Level, count(len(r.Nodes), "nodes", nl))
	if len(r.Validators) > 0 {
		s += ", " + count(len(r.Validators), "validators", vl)
	}
	if r.Consensus != nil && r.Consensus.Level != analysis.OK {
		s += "; consensus " + r.Consensus.Summary
	}
	if r.Upgrade != nil {
		s += "; " + r.Upgrade.Summary
	}
	if len(attention) > 0 {
		s += " — needs attention: " + strings.Join(attention, ", ")
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (r *OverviewReport) GetSummary() string { return r.Summary }
