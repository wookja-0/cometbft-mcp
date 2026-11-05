package analysis

import (
	"fmt"
	"strings"
	"time"
)

// HasQuorum reports whether power is more than 2/3 of total, the threshold
// CometBFT needs for a polka or a commit.
func HasQuorum(power, total int64) bool {
	return total > 0 && power*3 > total*2
}

type Consensus struct {
	Now        time.Time
	Height     int64
	Round      int32
	StartTime  time.Time // when the node started this height
	TotalPower int64
	// Current round: power whose vote (for a block or nil) has been received.
	PrevotePower   int64
	PrecommitPower int64
	// Validators with no prevote in the current round, already labeled.
	MissingPrevotes []string
	MissingPower    int64
	StuckAfter      time.Duration
}

// ManyRounds flags a height that needed several rounds even if it is moving.
const ManyRounds = 3

func JudgeConsensus(c Consensus) []Finding {
	elapsed := max(c.Now.Sub(c.StartTime), 0).Round(time.Second)
	stuck := elapsed >= c.StuckAfter
	pct := func(p int64) float64 { return float64(p) * 100 / float64(max(c.TotalPower, 1)) }

	if !stuck {
		if c.Round >= ManyRounds {
			return []Finding{{Warn, "many_rounds", fmt.Sprintf("height %d is in round %d: proposals keep failing (slow or offline proposers, or network trouble)", c.Height, c.Round)}}
		}
		return nil
	}

	where := fmt.Sprintf("height %d has been stuck for %s (round %d)", c.Height, elapsed, c.Round)
	if !HasQuorum(c.PrevotePower, c.TotalPower) {
		missing := "no validator is identifiable"
		if len(c.MissingPrevotes) > 0 {
			missing = strings.Join(c.MissingPrevotes, ", ")
		}
		return []Finding{{Critical, "no_quorum", fmt.Sprintf(
			"%s: only %.1f%% of voting power is voting, and more than 66.7%% is needed. Not voting (%.1f%% of power): %s",
			where, pct(c.PrevotePower), pct(c.MissingPower), missing)}}
	}
	return []Finding{{Critical, "stuck_with_quorum", fmt.Sprintf(
		"%s although %.1f%% of voting power is prevoting and %.1f%% precommitting: validators are online but not committing a block — "+
			"check node logs for an upgrade halt, an app hash or consensus error, or a proposer that cannot build a block",
		where, pct(c.PrevotePower), pct(c.PrecommitPower))}}
}
