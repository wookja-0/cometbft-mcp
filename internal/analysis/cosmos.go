package analysis

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// MaxMissed reproduces x/slashing: a validator is jailed for downtime when
// it misses more than window - round(window * min_signed_per_window) blocks,
// where round is LegacyDec.RoundInt64 (half to even).
func MaxMissed(window int64, minSignedPerWindow string) (int64, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(minSignedPerWindow))
	if !ok || r.Sign() < 0 || r.Cmp(big.NewRat(1, 1)) > 0 {
		return 0, errors.New("min_signed_per_window is not a decimal between 0 and 1")
	}
	if window <= 0 {
		return 0, errors.New("signed_blocks_window is not positive")
	}
	return window - roundHalfEven(new(big.Rat).Mul(r, big.NewRat(window, 1))), nil
}

func roundHalfEven(r *big.Rat) int64 {
	q, m := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int)) // r ≥ 0
	twice := new(big.Int).Mul(m, big.NewInt(2))
	switch twice.Cmp(r.Denom()) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	return q.Int64()
}

// ---- downtime slashing -------------------------------------------------

type Slashing struct {
	Now         time.Time
	Height      int64 // chain head
	Window      int64
	MaxMissed   int64
	Missed      int64 // missed_blocks_counter
	StartHeight int64
	Jailed      bool
	JailedUntil time.Time
	Tombstoned  bool
	Status      string // BOND_STATUS_*; empty if the staking lookup failed
	OutOfSet    bool   // not in the current CometBFT validator set; decides when Status is empty
	MissStreak  int    // consecutive misses at the head, from liveness
	BlockTime   time.Duration
}

type SlashingView struct {
	MissesUntilJail  int64         // further misses tolerated; the next one after this jails
	GraceUntilHeight int64         // downtime jail cannot trigger at or before this height; 0 if past
	TimeToJail       time.Duration // only when the validator is not signing right now (MissStreak ≥ StreakCritical)
	Untracked        bool          // not bonded: x/slashing does not count its blocks, so there is no margin
}

// Margins below these fractions of MaxMissed raise warn / critical.
const (
	JailRiskWarn     = 0.5
	JailRiskCritical = 0.1
)

func JudgeSlashing(s Slashing) (SlashingView, []Finding) {
	var v SlashingView
	var fs []Finding
	switch {
	case s.Tombstoned:
		return v, append(fs, Finding{Critical, "tombstoned", "tombstoned for double-signing: the validator is permanently removed and can never be unjailed"})
	case s.Jailed && s.JailedUntil.After(s.Now):
		return v, append(fs, Finding{Critical, "jailed", fmt.Sprintf("jailed until %s (in %s); after that it rejoins only with an unjail transaction", s.JailedUntil.UTC().Format(time.RFC3339), s.JailedUntil.Sub(s.Now).Round(time.Second))})
	case s.Jailed:
		return v, append(fs, Finding{Critical, "jailed", fmt.Sprintf("jailed; the jail period ended at %s — send an unjail transaction to rejoin", s.JailedUntil.UTC().Format(time.RFC3339))})
	}
	// x/slashing's BeginBlocker only handles the last commit's votes
	// (sdkCtx.VoteInfos()), i.e. the bonded set, so a validator outside it
	// accrues no misses and cannot be jailed for downtime:
	// https://github.com/cosmos/cosmos-sdk/blob/v0.53.0/x/slashing/abci.go
	if s.Status != "" && s.Status != "BOND_STATUS_BONDED" {
		v.Untracked = true
		return v, append(fs, Finding{Warn, "not_bonded", fmt.Sprintf("staking status is %s: not in the active set (not jailed, so most likely too little stake); no downtime jail risk while out of the set", bondStatus(s.Status))})
	}
	// Without a staking status the CometBFT set is the same fact: the votes
	// x/slashing handles are that set's. Liveness already reports the absence.
	if s.Status == "" && s.OutOfSet {
		v.Untracked = true
		return v, fs
	}

	v.MissesUntilJail = max(s.MaxMissed-s.Missed, 0)
	// x/slashing jails only while processing a block past minHeight; the next
	// block is Height+1, so the grace window ends once Height reaches it.
	if minHeight := s.StartHeight + s.Window; s.Height < minHeight {
		v.GraceUntilHeight = minHeight
		if s.Missed > s.MaxMissed {
			fs = append(fs, Finding{Warn, "jail_after_grace", fmt.Sprintf("already over the miss limit (%d > %d); it will be jailed after height %d unless missed blocks roll out of the window first", s.Missed, s.MaxMissed, minHeight)})
		}
		return v, fs
	}

	detail := fmt.Sprintf("missed %d of the last %d blocks; jailed above %d", s.Missed, s.Window, s.MaxMissed)
	switch frac := float64(v.MissesUntilJail) / float64(max(s.MaxMissed, 1)); {
	case frac <= JailRiskCritical:
		fs = append(fs, Finding{Critical, "jail_imminent", fmt.Sprintf("%d more missed blocks until downtime jail (%s)", v.MissesUntilJail, detail)})
	case frac <= JailRiskWarn:
		fs = append(fs, Finding{Warn, "jail_risk", fmt.Sprintf("%d more missed blocks until downtime jail (%s)", v.MissesUntilJail, detail)})
	}
	// Same bar as not_signing: one or two misses at the head can be a blip.
	if s.MissStreak >= StreakCritical && s.BlockTime > 0 {
		v.TimeToJail = time.Duration(v.MissesUntilJail+1) * s.BlockTime
		fs = append(fs, Finding{Critical, "time_to_jail", fmt.Sprintf("missing blocks right now: at %.1fs per block it will be jailed in about %s unless it resumes signing", s.BlockTime.Seconds(), v.TimeToJail.Round(time.Second))})
	}
	return v, fs
}

// bondStatus passes the staking enum through; anything else was not
// produced by x/staking and is labeled as such.
func bondStatus(s string) string {
	switch s {
	case "BOND_STATUS_BONDED", "BOND_STATUS_UNBONDING", "BOND_STATUS_UNBONDED", "BOND_STATUS_UNSPECIFIED":
		return s
	}
	return "unrecognized"
}

// ---- upgrades ------------------------------------------------------------

type Upgrade struct {
	Now        time.Time
	Height     int64
	HeadTime   time.Time
	BlockTime  time.Duration
	PlanName   string // already wrapped as untrusted by the caller
	PlanHeight int64  // 0 = no plan
	StaleAfter time.Duration
}

// UpgradeSoon is how far ahead an upgrade starts to warrant a warning.
const UpgradeSoon = 24 * time.Hour

// JudgeUpgrade returns the estimated upgrade time (zero if unknown or past).
func JudgeUpgrade(u Upgrade) (time.Time, []Finding) {
	if u.PlanHeight == 0 {
		return time.Time{}, nil
	}
	remaining := u.PlanHeight - u.Height
	if remaining <= 0 {
		if u.Now.Sub(u.HeadTime) >= u.StaleAfter {
			return time.Time{}, []Finding{{Critical, "upgrade_halt", fmt.Sprintf("the chain is halted at upgrade height %d for %s; every node must restart on the new binary", u.PlanHeight, u.PlanName)}}
		}
		return time.Time{}, []Finding{{Warn, "upgrade_height_reached", fmt.Sprintf("upgrade height %d for %s is reached; the chain halts for the upgrade now", u.PlanHeight, u.PlanName)}}
	}
	if u.BlockTime <= 0 {
		return time.Time{}, nil
	}
	eta := u.HeadTime.Add(time.Duration(remaining) * u.BlockTime)
	if in := eta.Sub(u.Now); in <= UpgradeSoon {
		return eta, []Finding{{Warn, "upgrade_soon", fmt.Sprintf("%s at height %d in about %s (%d blocks): make sure the new binary is staged on every node", u.PlanName, u.PlanHeight, in.Round(time.Minute), remaining)}}
	}
	return eta, nil
}

// JudgeVersions flags nodes of one chain running different app versions.
// versions maps node name to its reported version ("" = unknown).
func JudgeVersions(versions map[string]string) []Finding {
	byVersion := map[string][]string{}
	for node, v := range versions {
		if v != "" {
			byVersion[v] = append(byVersion[v], node)
		}
	}
	if len(byVersion) < 2 {
		return nil
	}
	var parts []string
	for v, nodes := range byVersion {
		sort.Strings(nodes)
		parts = append(parts, fmt.Sprintf("%s on %s", v, strings.Join(nodes, ", ")))
	}
	sort.Strings(parts)
	return []Finding{{Warn, "version_skew", "nodes run different app versions: " + strings.Join(parts, "; ")}}
}
