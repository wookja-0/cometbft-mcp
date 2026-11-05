package fleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
)

func TestConsensusHealthy(t *testing.T) {
	fc := &fakeChain{height: 100, set: fourVals, flag: allSigned, nodes: map[string]*fakeNode{
		"sentry": {atHeightFor: 500 * time.Millisecond},
	}}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}}, false)

	r, err := f.Consensus(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != analysis.OK || r.Height != 101 || r.Step != "Prevote" || r.TotalPower != 100 || !r.Prevotes.Quorum || r.Prevotes.ReceivedPct != 100 {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Validators) != 4 || r.Validators[0].Prevote != "block" || r.Proposer == "" {
		t.Fatalf("validators = %+v", r.Validators)
	}
}

func TestConsensusHaltNamesMissingValidators(t *testing.T) {
	// Powers in the fake set are 40/30/20/10: C and D offline leaves 70% —
	// above 2/3 — so take B and D offline instead: 60% online, no quorum.
	fc := &fakeChain{height: 100, set: fourVals, flag: allSigned, nodes: map[string]*fakeNode{
		"sentry": {
			atHeightFor: 3 * time.Minute,
			round:       9,
			seen:        func(a string) (bool, bool) { return a != addrB && a != addrD, false },
		},
	}}
	f := newFleet(t, fc, []config.Node{
		{Name: "sentry", Role: config.RoleSentry},
		{Name: "val-b", Role: config.RoleValidator, ValidatorAddress: addrB},
	}, false)

	r, err := f.Consensus(context.Background(), "sentry")
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != analysis.Critical || r.Prevotes.Quorum || r.Prevotes.ReceivedPct != 60 || r.Prevotes.Missing != 2 || r.Round != 9 {
		t.Fatalf("report = %+v", r)
	}
	fs := codesOf(r.Findings)
	if fs["no_quorum"] != analysis.Critical {
		t.Fatalf("findings = %v", fs)
	}
	// Our validator is named by its config name; the other by address prefix.
	if !strings.Contains(r.Summary, "not voting: val-b, DDDDDDDDDDDD…") {
		t.Fatalf("summary = %s", r.Summary)
	}
}

func TestConsensusStuckWithQuorum(t *testing.T) {
	fc := &fakeChain{height: 100, set: fourVals, flag: allSigned, nodes: map[string]*fakeNode{
		"sentry": {atHeightFor: 10 * time.Minute, seen: func(string) (bool, bool) { return true, false }},
	}}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}}, false)
	r, err := f.Consensus(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if codesOf(r.Findings)["stuck_with_quorum"] != analysis.Critical || !r.Prevotes.Quorum || r.Precommits.Quorum {
		t.Fatalf("report = %+v", r)
	}
}

func TestConsensusUnknownNode(t *testing.T) {
	fc := &fakeChain{height: 10, set: fourVals, flag: allSigned, nodes: map[string]*fakeNode{"s": {}}}
	f := newFleet(t, fc, []config.Node{{Name: "s", Role: config.RoleSentry}}, false)
	if _, err := f.Consensus(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("err = %v", err)
	}
}

func TestListConsensusValidatorsTrimsLargeSets(t *testing.T) {
	var all []ConsensusValidator
	for i := range 120 {
		v := ConsensusValidator{Address: strings.Repeat("A", 40), PowerPct: float64(120 - i), Prevote: "block", Precommit: "block"}
		if i%10 == 0 {
			v.Prevote = "missing"
		}
		if i == 77 {
			v.Node = "ours"
		}
		all = append(all, v)
	}
	kept, omitted := listConsensusValidators(all)
	if len(kept) != 13 || omitted != 107 {
		t.Fatalf("kept %d, omitted %d", len(kept), omitted)
	}
}
