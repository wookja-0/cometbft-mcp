package analysis

import (
	"testing"
	"time"
)

func codes(fs []Finding) map[string]Level {
	m := map[string]Level{}
	for _, f := range fs {
		m[f.Code] = f.Level
	}
	return m
}

func TestWorst(t *testing.T) {
	if Worst() != OK || Worst(OK, Unknown) != Unknown || Worst(Warn, Unknown) != Warn || Worst(Warn, Critical, OK) != Critical {
		t.Fatal("ranking broken")
	}
}

func TestJudgeSync(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	th := DefaultThresholds

	if fs := JudgeSync(Sync{LatestHeight: 100, LatestTime: now.Add(-3 * time.Second), ReferenceHeight: 101}, now, th); len(fs) != 0 {
		t.Fatalf("healthy node: %+v", fs)
	}

	fs := codes(JudgeSync(Sync{CatchingUp: true, LatestHeight: 40, LatestTime: now.Add(-2 * time.Minute), ReferenceHeight: 100}, now, th))
	if fs["catching_up"] != Warn || fs["stale_head"] != Warn || fs["behind_fleet"] != Critical {
		t.Fatalf("got %v", fs)
	}

	fs = codes(JudgeSync(Sync{LatestHeight: 100, LatestTime: now.Add(-10 * time.Minute)}, now, th))
	if fs["stale_head"] != Critical {
		t.Fatalf("got %v", fs)
	}
}

func TestJudgePeers(t *testing.T) {
	cases := []struct {
		name string
		in   Peers
		want map[string]Level
	}{
		{"isolated", Peers{Total: 0}, map[string]Level{"no_peers": Critical}},
		{"validator behind sentry", Peers{Validator: true, Listening: true, Total: 1, Inbound: 1}, map[string]Level{}},
		{"validator exposed", Peers{Validator: true, Listening: true, Total: 3, Outbound: 3, PublicPeers: 2}, map[string]Level{"validator_public_peers": Warn}},
		{"healthy sentry", Peers{Listening: true, Total: 8, Inbound: 4, Outbound: 4, PublicPeers: 8}, map[string]Level{}},
		{"thin sentry", Peers{Listening: false, Total: 2, Inbound: 2}, map[string]Level{"few_peers": Warn, "no_outbound_peers": Warn, "not_listening": Warn}},
	}
	for _, c := range cases {
		got := codes(JudgePeers(c.in))
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	}
}

func heights(from, n int) []int64 {
	h := make([]int64, n)
	for i := range h {
		h[i] = int64(from + i)
	}
	return h
}

func repeat(s SigStatus, n int) []SigStatus {
	out := make([]SigStatus, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestSummarizeHealthy(t *testing.T) {
	st := append(repeat(SigSigned, 98), SigNil, SigSigned)
	l := Summarize(heights(1, 100), st)
	if l.From != 1 || l.To != 100 || l.Signed != 99 || l.NilVotes != 1 || l.Missed != 0 || l.LastSignedHeight != 100 || l.MissStreak != 0 {
		t.Fatalf("got %+v", l)
	}
	if fs := JudgeLiveness(l); len(fs) != 0 {
		t.Fatalf("findings: %+v", fs)
	}
}

func TestSummarizeStoppedSigning(t *testing.T) {
	// Signed 90, then down for the last 10; one unreadable commit in the run.
	st := append(repeat(SigSigned, 90), repeat(SigMissed, 10)...)
	st[95] = SigUnknown
	l := Summarize(heights(1, 100), st)
	if l.Missed != 9 || l.MissStreak != 9 || l.LastSignedHeight != 90 || l.Unknown != 1 {
		t.Fatalf("got %+v", l)
	}
	if l.MissedPct < 9.0 || l.MissedPct > 9.1 {
		t.Fatalf("pct = %f", l.MissedPct)
	}
	fs := codes(JudgeLiveness(l))
	if fs["not_signing"] != Critical || fs["missed_blocks"] != Warn || fs["partial_data"] != Unknown {
		t.Fatalf("got %v", fs)
	}
}

func TestSummarizeLeftSet(t *testing.T) {
	st := append(append(repeat(SigSigned, 40), repeat(SigMissed, 30)...), repeat(SigNotInSet, 30)...)
	l := Summarize(heights(1, 100), st)
	if l.LeftSetAt != 71 || l.MissStreak != 0 || l.NotInSet != 30 {
		t.Fatalf("got %+v", l)
	}
	if fs := codes(JudgeLiveness(l)); fs["left_active_set"] != Critical || fs["missed_blocks"] != Warn {
		t.Fatalf("got %v", fs)
	}
}

func TestSummarizeNeverInSet(t *testing.T) {
	l := Summarize(heights(1, 50), repeat(SigNotInSet, 50))
	if l.LeftSetAt != 0 {
		t.Fatalf("got %+v", l)
	}
	if fs := codes(JudgeLiveness(l)); fs["not_in_active_set"] != Warn || len(fs) != 1 {
		t.Fatalf("got %v", fs)
	}
}

func TestJudgeLivenessNoData(t *testing.T) {
	l := Summarize(heights(1, 5), repeat(SigUnknown, 5))
	if fs := codes(JudgeLiveness(l)); fs["no_data"] != Unknown || len(fs) != 1 {
		t.Fatalf("got %v", fs)
	}
}
