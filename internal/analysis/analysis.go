// Package analysis turns raw node data into operator judgments. Everything
// here is a pure function: no I/O, no clock reads — callers pass `now`.
package analysis

import (
	"fmt"
	"time"
)

type Level string

const (
	OK       Level = "ok"
	Unknown  Level = "unknown"
	Warn     Level = "warn"
	Critical Level = "critical"
)

var rank = map[Level]int{OK: 0, Unknown: 1, Warn: 2, Critical: 3}

// Worst returns the most severe level; OK when given none.
func Worst(levels ...Level) Level {
	w := OK
	for _, l := range levels {
		if rank[l] > rank[w] {
			w = l
		}
	}
	return w
}

// MoreSevere reports whether l ranks above o.
func (l Level) MoreSevere(o Level) bool { return rank[l] > rank[o] }

// Finding is one judgment with a stable machine-readable code.
type Finding struct {
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LevelOf returns the worst level among findings.
func LevelOf(fs []Finding) Level {
	w := OK
	for _, f := range fs {
		w = Worst(w, f.Level)
	}
	return w
}

// ---- sync ----------------------------------------------------------------

type Thresholds struct {
	StaleWarn     time.Duration // no new block for this long
	StaleCritical time.Duration
	LagWarn       int64 // blocks behind the highest node in the fleet
	LagCritical   int64
}

var DefaultThresholds = Thresholds{
	StaleWarn:     time.Minute,
	StaleCritical: 5 * time.Minute,
	LagWarn:       5,
	LagCritical:   50,
}

type Sync struct {
	CatchingUp      bool
	LatestHeight    int64
	LatestTime      time.Time
	ReferenceHeight int64 // highest height seen in the fleet; 0 if unknown
}

func JudgeSync(s Sync, now time.Time, th Thresholds) []Finding {
	var fs []Finding
	if s.CatchingUp {
		fs = append(fs, Finding{Warn, "catching_up", "node reports catching_up=true; it is still syncing and not following the chain head"})
	}
	if !s.LatestTime.IsZero() {
		age := now.Sub(s.LatestTime).Round(time.Second)
		switch {
		case age >= th.StaleCritical:
			fs = append(fs, Finding{Critical, "stale_head", fmt.Sprintf("no new block for %s (critical ≥ %s); the node or the chain is halted", age, th.StaleCritical)})
		case age >= th.StaleWarn:
			fs = append(fs, Finding{Warn, "stale_head", fmt.Sprintf("no new block for %s (warn ≥ %s)", age, th.StaleWarn)})
		}
	}
	if s.ReferenceHeight > 0 {
		lag := s.ReferenceHeight - s.LatestHeight
		switch {
		case lag >= th.LagCritical:
			fs = append(fs, Finding{Critical, "behind_fleet", fmt.Sprintf("%d blocks behind the highest node in the fleet (critical ≥ %d)", lag, th.LagCritical)})
		case lag >= th.LagWarn:
			fs = append(fs, Finding{Warn, "behind_fleet", fmt.Sprintf("%d blocks behind the highest node in the fleet (warn ≥ %d)", lag, th.LagWarn)})
		}
	}
	return fs
}

// ---- peers ---------------------------------------------------------------

type Peers struct {
	Validator   bool // node role is validator
	Listening   bool
	Total       int
	Inbound     int
	Outbound    int
	PublicPeers int // peers whose remote address is globally routable
}

// MinPeers is the floor for sentries and full nodes. Validators behind
// sentries legitimately run with one or two peers.
const MinPeers = 3

func JudgePeers(p Peers) []Finding {
	var fs []Finding
	if p.Total == 0 {
		return append(fs, Finding{Critical, "no_peers", "node has no peers; it cannot receive blocks or gossip votes"})
	}
	if p.Validator {
		if p.PublicPeers > 0 {
			fs = append(fs, Finding{Warn, "validator_public_peers", fmt.Sprintf("%d of %d peers are on public addresses; if this validator is meant to sit behind sentries, the topology is being bypassed and its address may be exposed", p.PublicPeers, p.Total)})
		}
		return fs
	}
	if p.Total < MinPeers {
		fs = append(fs, Finding{Warn, "few_peers", fmt.Sprintf("only %d peer(s) (warn < %d); one more disconnect could isolate the node", p.Total, MinPeers)})
	}
	if p.Outbound == 0 {
		fs = append(fs, Finding{Warn, "no_outbound_peers", "no outbound connections; the node depends entirely on others dialing in (check persistent_peers / seeds)"})
	}
	if !p.Listening {
		fs = append(fs, Finding{Warn, "not_listening", "p2p listener is not accepting connections"})
	}
	return fs
}

// ---- liveness ------------------------------------------------------------

type SigStatus int

const (
	SigUnknown  SigStatus = iota // commit could not be read
	SigSigned                    // signed for the block
	SigNil                       // voted nil; counts as signed for downtime slashing
	SigMissed                    // in the set, signature absent
	SigNotInSet                  // not in the validator set at that height
)

type Liveness struct {
	From, To         int64
	Blocks           int
	Signed           int
	NilVotes         int
	Missed           int
	NotInSet         int
	Unknown          int
	MissedPct        float64 // missed / heights where the validator was in the set
	MissStreak       int     // consecutive missed blocks ending at To
	LastSignedHeight int64
	LeftSetAt        int64 // first height of a trailing not-in-set run that followed in-set heights; 0 if none
}

// Summarize folds per-height statuses (ascending heights) into a Liveness.
func Summarize(heights []int64, statuses []SigStatus) Liveness {
	var l Liveness
	if len(heights) == 0 {
		return l
	}
	l.From, l.To, l.Blocks = heights[0], heights[len(heights)-1], len(heights)
	seenInSet := false
	for i, s := range statuses {
		switch s {
		case SigSigned, SigNil:
			if s == SigSigned {
				l.Signed++
			} else {
				l.NilVotes++
			}
			l.LastSignedHeight = heights[i]
			seenInSet = true
		case SigMissed:
			l.Missed++
			seenInSet = true
		case SigNotInSet:
			l.NotInSet++
		default:
			l.Unknown++
		}
	}
	if inSet := l.Signed + l.NilVotes + l.Missed; inSet > 0 {
		l.MissedPct = float64(l.Missed) * 100 / float64(inSet)
	}
	// Trailing runs, skipping unreadable heights.
	for i := len(statuses) - 1; i >= 0; i-- {
		if statuses[i] == SigUnknown {
			continue
		}
		if statuses[i] != SigMissed {
			break
		}
		l.MissStreak++
	}
	if seenInSet {
		for i := len(statuses) - 1; i >= 0; i-- {
			if statuses[i] == SigUnknown {
				continue // an unreadable commit does not end the run
			}
			if statuses[i] != SigNotInSet {
				break
			}
			l.LeftSetAt = heights[i]
		}
	}
	return l
}

// StreakCritical is how many consecutive misses at the head mean "not
// signing right now" rather than a transient blip.
const StreakCritical = 3

// MissedPctWarn flags a validator missing a noticeable share of blocks.
// Whether that is close to jail depends on the chain's slashing params,
// which need the Cosmos SDK layer; this is a chain-agnostic early signal.
const MissedPctWarn = 5.0

func JudgeLiveness(l Liveness) []Finding {
	var fs []Finding
	inSet := l.Signed + l.NilVotes + l.Missed
	switch {
	case l.Blocks == 0 || l.Unknown == l.Blocks:
		return append(fs, Finding{Unknown, "no_data", "no commits in the window could be read"})
	case l.LeftSetAt > 0:
		fs = append(fs, Finding{Critical, "left_active_set", fmt.Sprintf("dropped out of the validator set at height %d (jailed, unbonded or tombstoned)", l.LeftSetAt)})
	case inSet == 0:
		fs = append(fs, Finding{Warn, "not_in_active_set", "not in the validator set at any height in the window; check the address, or the validator is jailed/inactive"})
	}
	if l.MissStreak >= StreakCritical {
		fs = append(fs, Finding{Critical, "not_signing", fmt.Sprintf("missed the last %d blocks in a row; the validator is not signing right now", l.MissStreak)})
	}
	if l.MissedPct >= MissedPctWarn {
		fs = append(fs, Finding{Warn, "missed_blocks", fmt.Sprintf("missed %d of %d blocks in the window (%.1f%%, warn ≥ %.0f%%)", l.Missed, inSet, l.MissedPct, MissedPctWarn)})
	}
	if l.Unknown > 0 {
		fs = append(fs, Finding{Unknown, "partial_data", fmt.Sprintf("%d of %d commits could not be read (pruned or RPC errors)", l.Unknown, l.Blocks)})
	}
	return fs
}
