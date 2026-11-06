package analysis

import (
	"strings"
	"testing"
	"time"
)

func TestMaxMissed(t *testing.T) {
	cases := []struct {
		window int64
		min    string
		want   int64
	}{
		{10000, "0.050000000000000000", 9500}, // Cosmos Hub
		{50, "0.500000000000000000", 25},      // localnet
		{100, "0.000000000000000000", 100},
		{100, "1.000000000000000000", 0},
		{5, "0.500000000000000000", 3}, // 2.5 rounds half-even to 2
		{7, "0.500000000000000000", 3}, // 3.5 rounds half-even to 4
		{3, "0.333333333333333333", 2}, // 0.999… rounds to 1
		{30000, "0.05", 28500},
	}
	for _, c := range cases {
		got, err := MaxMissed(c.window, c.min)
		if err != nil || got != c.want {
			t.Errorf("MaxMissed(%d, %s) = %d, %v; want %d", c.window, c.min, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "-0.1", "1.5"} {
		if _, err := MaxMissed(100, bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

var t0 = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func base() Slashing {
	return Slashing{Now: t0, Height: 1000, Window: 50, MaxMissed: 25, StartHeight: 1, Status: "BOND_STATUS_BONDED", BlockTime: time.Second}
}

func TestJudgeSlashingHealthy(t *testing.T) {
	s := base()
	s.Missed = 3
	v, fs := JudgeSlashing(s)
	if len(fs) != 0 || v.MissesUntilJail != 22 || v.GraceUntilHeight != 0 {
		t.Fatalf("view = %+v, findings = %+v", v, fs)
	}
}

func TestJudgeSlashingMargins(t *testing.T) {
	s := base()
	s.Missed = 15 // 10 left of 25 → 40% → warn
	if _, fs := JudgeSlashing(s); codes(fs)["jail_risk"] != Warn {
		t.Fatalf("got %v", codes(fs))
	}
	s.Missed = 23 // 2 left → 8% → critical
	if _, fs := JudgeSlashing(s); codes(fs)["jail_imminent"] != Critical {
		t.Fatalf("got %v", codes(fs))
	}
}

func TestJudgeSlashingTimeToJail(t *testing.T) {
	s := base()
	s.Missed, s.MissStreak, s.BlockTime = 20, 20, 6*time.Second
	v, fs := JudgeSlashing(s)
	if v.MissesUntilJail != 5 || v.TimeToJail != 36*time.Second {
		t.Fatalf("view = %+v", v)
	}
	f := codes(fs)
	if f["time_to_jail"] != Critical || f["jail_risk"] != Warn {
		t.Fatalf("got %v", f)
	}
}

func TestJudgeSlashingShortStreakHasNoTimeToJail(t *testing.T) {
	s := base()
	s.Missed, s.MissStreak = 2, StreakCritical-1
	if v, fs := JudgeSlashing(s); v.TimeToJail != 0 || len(fs) != 0 {
		t.Fatalf("view = %+v, findings = %v", v, codes(fs))
	}
}

func TestJudgeSlashingJailedAndTombstoned(t *testing.T) {
	s := base()
	s.Jailed, s.JailedUntil, s.Missed = true, t0.Add(90*time.Second), 0
	_, fs := JudgeSlashing(s)
	if len(fs) != 1 || fs[0].Code != "jailed" || !strings.Contains(fs[0].Message, "in 1m30s") {
		t.Fatalf("got %+v", fs)
	}
	s.JailedUntil = t0.Add(-time.Minute)
	if _, fs := JudgeSlashing(s); !strings.Contains(fs[0].Message, "send an unjail transaction") {
		t.Fatalf("got %+v", fs)
	}
	s.Tombstoned = true
	if _, fs := JudgeSlashing(s); fs[0].Code != "tombstoned" {
		t.Fatalf("got %+v", fs)
	}
}

func TestJudgeSlashingGrace(t *testing.T) {
	s := base()
	s.StartHeight, s.Height, s.Missed = 980, 1000, 26
	v, fs := JudgeSlashing(s)
	if v.GraceUntilHeight != 1030 || codes(fs)["jail_after_grace"] != Warn || codes(fs)["jail_imminent"] != "" {
		t.Fatalf("view = %+v, findings = %v", v, codes(fs))
	}
}

func TestJudgeSlashingNotBonded(t *testing.T) {
	s := base()
	s.Status = "BOND_STATUS_UNBONDED"
	if _, fs := JudgeSlashing(s); codes(fs)["not_bonded"] != Warn {
		t.Fatalf("got %v", codes(fs))
	}
	// Seen on Cosmos Hub: a validator pushed out of the set by stake, with
	// misses from before it left. Out of the set it cannot be jailed for
	// downtime, so there is no margin or time to jail.
	s.Status, s.Missed, s.MissStreak = "BOND_STATUS_UNBONDING", 24, 20
	v, fs := JudgeSlashing(s)
	if !v.Untracked || v.TimeToJail != 0 || len(fs) != 1 || fs[0].Code != "not_bonded" {
		t.Fatalf("view = %+v, findings = %v", v, codes(fs))
	}
}

// Without a staking status (the lookup failed), the CometBFT validator set
// decides: out of it, x/slashing does not count the validator either.
func TestJudgeSlashingOutOfSetWithoutStakingStatus(t *testing.T) {
	s := base()
	s.Status, s.OutOfSet, s.Missed, s.MissStreak = "", true, 24, 20
	v, fs := JudgeSlashing(s)
	if !v.Untracked || v.TimeToJail != 0 || len(fs) != 0 {
		t.Fatalf("view = %+v, findings = %v", v, codes(fs))
	}
	// A known staking status wins over the set (it is the chain's own view).
	s.Status = "BOND_STATUS_BONDED"
	if v, _ := JudgeSlashing(s); v.Untracked {
		t.Fatalf("view = %+v", v)
	}
}

func TestJudgeUpgrade(t *testing.T) {
	u := Upgrade{Now: t0, Height: 1000, HeadTime: t0.Add(-2 * time.Second), BlockTime: 6 * time.Second, PlanName: "<untrusted>v2</untrusted>", StaleAfter: time.Minute}

	if eta, fs := JudgeUpgrade(u); !eta.IsZero() || fs != nil {
		t.Fatal("no plan should be silent")
	}

	u.PlanHeight = 1600 // 600 blocks × 6s = 1h
	eta, fs := JudgeUpgrade(u)
	if !eta.Equal(t0.Add(-2*time.Second+time.Hour)) || codes(fs)["upgrade_soon"] != Warn {
		t.Fatalf("eta = %s, findings = %v", eta, codes(fs))
	}

	u.PlanHeight = 100000 // ~6.6 days
	if _, fs := JudgeUpgrade(u); len(fs) != 0 {
		t.Fatalf("distant upgrade: %v", codes(fs))
	}

	u.PlanHeight = 1000
	if _, fs := JudgeUpgrade(u); codes(fs)["upgrade_height_reached"] != Warn {
		t.Fatalf("got %v", codes(fs))
	}
	u.HeadTime = t0.Add(-5 * time.Minute)
	if _, fs := JudgeUpgrade(u); codes(fs)["upgrade_halt"] != Critical {
		t.Fatalf("got %v", codes(fs))
	}
}

func TestJudgeVersions(t *testing.T) {
	if fs := JudgeVersions(map[string]string{"a": "1.0", "b": "1.0", "c": ""}); fs != nil {
		t.Fatalf("got %v", fs)
	}
	fs := JudgeVersions(map[string]string{"a": "1.0", "b": "1.1", "c": "1.0"})
	if len(fs) != 1 || fs[0].Message != "nodes run different app versions: 1.0 on a, c; 1.1 on b" {
		t.Fatalf("got %+v", fs)
	}
}

// x/slashing can jail while processing the block after StartHeight+Window,
// so once the head reaches that height the grace window is over.
func TestJudgeSlashingGraceBoundary(t *testing.T) {
	s := base()
	s.StartHeight, s.Window, s.MaxMissed, s.Missed = 950, 50, 25, 24
	s.Height = 999 // the next block is 1000 = minHeight: still cannot jail
	if v, _ := JudgeSlashing(s); v.GraceUntilHeight != 1000 {
		t.Fatalf("height 999: %+v", v)
	}
	s.Height = 1000 // the next block is 1001 > minHeight: can jail
	if v, fs := JudgeSlashing(s); v.GraceUntilHeight != 0 || codes(fs)["jail_imminent"] != Critical {
		t.Fatalf("height 1000: %+v %v", v, codes(fs))
	}
}
