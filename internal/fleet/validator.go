package fleet

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

const (
	DefaultWindow = 100
	MinWindow     = 10
	MaxWindow     = 500
)

type ValidatorReport struct {
	Summary  string             `json:"summary"`
	Level    analysis.Level     `json:"level"`
	Findings []analysis.Finding `json:"findings"`

	Validator     string `json:"validator"`
	Address       string `json:"address"`
	AddressSource string `json:"address_source"` // "config" or "node"
	SourceNode    string `json:"source_node"`    // node the commits were read from

	Window           Window           `json:"window"`
	Signed           int              `json:"signed"`
	NilVotes         int              `json:"nil_votes"`
	Missed           int              `json:"missed"`
	NotInSet         int              `json:"not_in_set"`
	Unreadable       int              `json:"unreadable"`
	MissedPct        float64          `json:"missed_pct"`
	MissStreak       int              `json:"miss_streak"`
	LastSignedHeight int64            `json:"last_signed_height,omitempty"`
	ActiveSet        *ActiveSet       `json:"active_set,omitempty"`
	AvgBlockSeconds  float64          `json:"avg_block_seconds,omitempty"`
	Cosmos           *CosmosValidator `json:"cosmos,omitempty"`
	Note             string           `json:"note"`
}

type Window struct {
	From   int64 `json:"from"`
	To     int64 `json:"to"`
	Blocks int   `json:"blocks"`
}

type ActiveSet struct {
	InSet         bool    `json:"in_set"`
	VotingPower   int64   `json:"voting_power"`
	PowerSharePct float64 `json:"power_share_pct"`
	Rank          int     `json:"rank,omitempty"`
	SetSize       int     `json:"set_size"`
}

const (
	validatorNote = "Liveness is computed from commit signatures and works on any CometBFT chain. " +
		"Jail status and the exact margin to the chain's downtime-slashing threshold need the Cosmos SDK layer: set cosmos.rest in the config."
	validatorNoteCosmos = "Liveness is computed from commit signatures; jail status and the slashing margin come from the chain's x/slashing and x/staking state (cosmos section). " +
		"missed_in_window is the chain's own counter over its signing window, which can differ in length from the liveness window above."
)

// Validator reports signing liveness for a validator node over the last
// `window` blocks. Commits are read from a healthy non-validator node when
// one exists, so the check works while the validator itself is down.
func (f *Fleet) Validator(ctx context.Context, name string, window int) (*ValidatorReport, error) {
	target, err := f.pickValidator(name)
	if err != nil {
		return nil, err
	}
	window = clampWindow(window)

	addr, addrSource := target.ValidatorHex, "config"
	if addr == "" {
		s, err := f.clients[target.Name].Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("validator_address is not set for %q and its node did not answer (%s); set validator_address in the config so liveness can be checked while the node is down", target.Name, f.nodeError(err).Code)
		}
		addr, addrSource = strings.ToUpper(s.ValidatorInfo.Address), "node"
	}

	src, st, err := f.pickSource(ctx, target.Name)
	if err != nil {
		return nil, err
	}
	latest, earliest := int64(st.SyncInfo.LatestBlockHeight), int64(st.SyncInfo.EarliestBlockHeight)

	// The commit at the latest height is the node's "seen commit": it holds
	// only the precommits that arrived before +2/3, so a slower (e.g.
	// sentry-shielded) validator looks absent there. Canonical commits —
	// the ones downtime slashing counts — exist up to latest-1.
	to := latest - 1
	from := max(to-int64(window)+1, earliest, 1)
	if to < from {
		return nil, fmt.Errorf("chain on %s is at height %d; no canonical commits to scan yet", src.name, latest)
	}
	heights := make([]int64, 0, to-from+1)
	for h := from; h <= to; h++ {
		heights = append(heights, h)
	}

	scan := newScanner(src, addr)
	statuses := scan.run(ctx, heights)
	l := analysis.Summarize(heights, statuses)
	blockTime := scan.blockTime()

	r := &ValidatorReport{
		Validator:        target.Name,
		Address:          addr,
		AddressSource:    addrSource,
		SourceNode:       src.name,
		Window:           Window{From: l.From, To: l.To, Blocks: l.Blocks},
		Signed:           l.Signed,
		NilVotes:         l.NilVotes,
		Missed:           l.Missed,
		NotInSet:         l.NotInSet,
		Unreadable:       l.Unknown,
		MissedPct:        roundTo(l.MissedPct, 2),
		MissStreak:       l.MissStreak,
		LastSignedHeight: l.LastSignedHeight,
		Note:             validatorNote,
	}
	r.Findings = analysis.JudgeLiveness(l)
	if r.Findings == nil {
		r.Findings = []analysis.Finding{}
	}
	if set, err := scan.validatorsAt(ctx, latest); err == nil {
		r.ActiveSet = activeSet(set, addr)
	}
	if blockTime > 0 {
		r.AvgBlockSeconds = roundTo(blockTime.Seconds(), 2)
	}
	if f.cosmos != nil {
		cv, fs := f.cosmosValidator(ctx, addr, st.NodeInfo.Network, latest, l.MissStreak, blockTime)
		r.Cosmos = cv
		r.Findings = append(r.Findings, fs...)
		r.Note = validatorNoteCosmos
	}
	r.Level = analysis.LevelOf(r.Findings)
	r.Summary = validatorSummary(r)
	return r, nil
}

func (f *Fleet) pickValidator(name string) (*config.Node, error) {
	var vals []string
	for _, n := range f.cfg.Nodes {
		if n.Role == config.RoleValidator {
			vals = append(vals, n.Name)
		}
	}
	if name == "" {
		switch len(vals) {
		case 0:
			return nil, fmt.Errorf("no node has role: validator in the config")
		case 1:
			name = vals[0]
		default:
			return nil, fmt.Errorf("several validators are configured; pass node, one of: %s", strings.Join(vals, ", "))
		}
	}
	n, ok := f.cfg.Node(name)
	if !ok {
		return nil, fmt.Errorf("unknown node %q; validators: %s", name, strings.Join(vals, ", "))
	}
	if n.Role != config.RoleValidator {
		return nil, fmt.Errorf("node %q has role %s, not validator; validators: %s", name, n.Role, strings.Join(vals, ", "))
	}
	return n, nil
}

type source struct {
	name   string
	client *comet.Client
}

// pickSource returns the first synced node and its status, preferring
// sentries and full nodes (to keep load off validators) and target last.
func (f *Fleet) pickSource(ctx context.Context, target string) (source, *comet.Status, error) {
	var order []config.Node
	for _, pass := range []func(config.Node) bool{
		func(n config.Node) bool { return n.Role != config.RoleValidator },
		func(n config.Node) bool { return n.Role == config.RoleValidator && n.Name != target },
		func(n config.Node) bool { return n.Name == target },
	} {
		for _, n := range f.cfg.Nodes {
			if pass(n) {
				order = append(order, n)
			}
		}
	}
	var tried []string
	for _, n := range order {
		c := f.clients[n.Name]
		s, err := c.Status(ctx)
		if err != nil {
			tried = append(tried, n.Name+": "+f.nodeError(err).Code)
			continue
		}
		if s.SyncInfo.CatchingUp {
			tried = append(tried, n.Name+": catching_up")
			continue
		}
		return source{n.Name, c}, s, nil
	}
	return source{}, nil, fmt.Errorf("no synced node to read commits from (%s)", strings.Join(tried, "; "))
}

// scanner classifies the validator's signature at each height. A commit
// lists absent signatures without an address, so when the address is not
// found the validator set at that height (same order as the signatures)
// decides between "missed" and "not in the set".
type scanner struct {
	src  source
	addr string

	mu   sync.Mutex
	sets map[string][]comet.Validator // by validators_hash

	first, last *comet.Commit // earliest and latest readable commits, for block time
}

func newScanner(src source, addr string) *scanner {
	return &scanner{src: src, addr: addr, sets: map[string][]comet.Validator{}}
}

func (s *scanner) run(ctx context.Context, heights []int64) []analysis.SigStatus {
	commits := make([]*comet.Commit, len(heights))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // safehttp also caps global concurrency
	for i, h := range heights {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if c, err := s.src.client.Commit(ctx, h); err == nil {
				commits[i] = c
			}
		}()
	}
	wg.Wait()

	out := make([]analysis.SigStatus, len(heights))
	for i, c := range commits {
		out[i] = s.classify(ctx, c)
		if c != nil {
			if s.first == nil {
				s.first = c
			}
			s.last = c
		}
	}
	return out
}

// blockTime is the mean block interval over the scanned commits; 0 if unknown.
func (s *scanner) blockTime() time.Duration {
	if s.first == nil || s.last == nil {
		return 0
	}
	blocks := int64(s.last.Header.Height - s.first.Header.Height)
	span := s.last.Header.Time.Sub(s.first.Header.Time)
	if blocks <= 0 || span <= 0 {
		return 0
	}
	return span / time.Duration(blocks)
}

func (s *scanner) classify(ctx context.Context, c *comet.Commit) analysis.SigStatus {
	if c == nil {
		return analysis.SigUnknown
	}
	for _, sig := range c.Commit.Signatures {
		if strings.EqualFold(sig.ValidatorAddress, s.addr) {
			return flagStatus(sig.BlockIDFlag)
		}
	}
	set, err := s.setFor(ctx, int64(c.Header.Height), c.Header.ValidatorsHash)
	if err != nil {
		return analysis.SigUnknown
	}
	for i, v := range set {
		if strings.EqualFold(v.Address, s.addr) {
			if i >= len(c.Commit.Signatures) {
				return analysis.SigUnknown
			}
			return flagStatus(c.Commit.Signatures[i].BlockIDFlag)
		}
	}
	return analysis.SigNotInSet
}

func flagStatus(flag int) analysis.SigStatus {
	switch flag {
	case comet.FlagCommit:
		return analysis.SigSigned
	case comet.FlagNil:
		return analysis.SigNil
	case comet.FlagAbsent:
		return analysis.SigMissed
	default:
		return analysis.SigUnknown
	}
}

func (s *scanner) setFor(ctx context.Context, height int64, hash string) ([]comet.Validator, error) {
	s.mu.Lock()
	set, ok := s.sets[hash]
	s.mu.Unlock()
	if ok && hash != "" {
		return set, nil
	}
	set, err := s.src.client.Validators(ctx, height)
	if err != nil {
		return nil, err
	}
	if hash != "" {
		s.mu.Lock()
		s.sets[hash] = set
		s.mu.Unlock()
	}
	return set, nil
}

func (s *scanner) validatorsAt(ctx context.Context, height int64) ([]comet.Validator, error) {
	return s.src.client.Validators(ctx, height)
}

func activeSet(set []comet.Validator, addr string) *ActiveSet {
	as := &ActiveSet{SetSize: len(set)}
	var total int64
	for _, v := range set {
		total += int64(v.VotingPower)
	}
	for i, v := range set {
		if strings.EqualFold(v.Address, addr) {
			as.InSet = true
			as.VotingPower = int64(v.VotingPower)
			as.Rank = i + 1 // the set is sorted by voting power
			if total > 0 {
				as.PowerSharePct = roundTo(float64(v.VotingPower)*100/float64(total), 2)
			}
		}
	}
	return as
}

func validatorSummary(r *ValidatorReport) string {
	s := fmt.Sprintf("%s [%s]: signed %d/%d blocks (%d–%d), missed %d (%.2f%%)",
		r.Validator, r.Level, r.Signed+r.NilVotes, r.Signed+r.NilVotes+r.Missed, r.Window.From, r.Window.To, r.Missed, r.MissedPct)
	if r.MissStreak > 0 {
		s += fmt.Sprintf(", %d missed in a row at the head", r.MissStreak)
	}
	if r.ActiveSet != nil && !r.ActiveSet.InSet {
		s += ", not in the current validator set"
	}
	if c := r.Cosmos; c != nil && c.Error == nil {
		switch {
		case c.Tombstoned:
			s += "; TOMBSTONED"
		case c.Jailed:
			s += "; JAILED"
		case c.MissesUntilJail != nil:
			s += fmt.Sprintf("; %d more misses until downtime jail", *c.MissesUntilJail)
			if c.TimeToJail != "" {
				s += " (~" + c.TimeToJail + " at the current rate)"
			}
		}
	}
	return s
}

func clampWindow(w int) int {
	switch {
	case w <= 0:
		return DefaultWindow
	case w < MinWindow:
		return MinWindow
	case w > MaxWindow:
		return MaxWindow
	}
	return w
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for range places {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}
