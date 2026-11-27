package analysis

import (
	"strings"
	"testing"
	"time"
)

func TestHasQuorum(t *testing.T) {
	cases := []struct {
		p, total int64
		want     bool
	}{
		{300, 400, true}, {267, 400, true}, {266, 400, false}, // 2/3 of 400 = 266.67
		{200, 300, false}, {201, 300, true}, // exactly 2/3 is not enough
		{0, 0, false},
	}
	for _, c := range cases {
		if got := HasQuorum(c.p, c.total); got != c.want {
			t.Errorf("HasQuorum(%d, %d) = %v", c.p, c.total, got)
		}
	}
}

func cons() Consensus {
	return Consensus{Now: t0, Height: 500, TotalPower: 400, StartTime: t0.Add(-2 * time.Second), PrevotePower: 400, PrecommitPower: 400, StuckAfter: time.Minute}
}

func TestJudgeConsensusHealthy(t *testing.T) {
	if fs := JudgeConsensus(cons()); fs != nil {
		t.Fatalf("got %+v", fs)
	}
	c := cons()
	c.StartTime = t0.Add(time.Second) // timeout_commit not over yet: start time is in the future
	if fs := JudgeConsensus(c); fs != nil {
		t.Fatalf("got %+v", fs)
	}
}

func TestJudgeConsensusManyRounds(t *testing.T) {
	c := cons()
	c.Round = 4
	if fs := codes(JudgeConsensus(c)); fs["many_rounds"] != Warn {
		t.Fatalf("got %v", fs)
	}
}

func TestJudgeConsensusNoQuorum(t *testing.T) {
	c := cons()
	c.StartTime, c.Round = t0.Add(-3*time.Minute), 7
	c.PrevotePower, c.PrecommitPower = 200, 200
	c.MissingPrevotes, c.MissingPower = []string{"val2", "val3"}, 200
	fs := JudgeConsensus(c)
	if len(fs) != 1 || fs[0].Code != "no_quorum" || fs[0].Level != Critical {
		t.Fatalf("got %+v", fs)
	}
	for _, want := range []string{"height 500 has been stuck for 3m0s (round 7)", "only 50.0%", "(50.0% of power): val2, val3"} {
		if !strings.Contains(fs[0].Message, want) {
			t.Errorf("message lacks %q: %s", want, fs[0].Message)
		}
	}
}

func TestJudgeConsensusStuckWithQuorum(t *testing.T) {
	c := cons()
	c.StartTime = t0.Add(-10 * time.Minute)
	// Everyone prevoted and nobody precommits, or enough precommitted and
	// still no block: the validators are up but the chain is not moving.
	for _, precommit := range []int64{0, 300} {
		c.PrecommitPower = precommit
		if fs := codes(JudgeConsensus(c)); fs["stuck_with_quorum"] != Critical {
			t.Fatalf("precommit %d: got %v", precommit, fs)
		}
	}
}

// Seen in the e2e halt test: val2 prevoted, then stopped before it could
// precommit. Prevotes have a quorum, precommits never will, and the ones
// missing from the precommits are what blocks the chain.
func TestJudgeConsensusNoPrecommitQuorum(t *testing.T) {
	c := cons()
	c.StartTime = t0.Add(-time.Minute)
	c.PrevotePower, c.PrecommitPower = 300, 200
	c.MissingPrevotes, c.MissingPower = []string{"val3"}, 100
	c.MissingPrecommits, c.MissingPrecommitPower = []string{"val2", "val3"}, 200
	fs := JudgeConsensus(c)
	if len(fs) != 1 || fs[0].Code != "no_quorum" || fs[0].Level != Critical {
		t.Fatalf("got %+v", fs)
	}
	for _, want := range []string{"75.0% of voting power prevoted but only 50.0% precommitted", "Not precommitting (50.0% of power): val2, val3"} {
		if !strings.Contains(fs[0].Message, want) {
			t.Errorf("message lacks %q: %s", want, fs[0].Message)
		}
	}
}
