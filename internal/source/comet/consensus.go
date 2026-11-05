package comet

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
)

// RoundState is the node's live consensus state (/consensus_state).
type RoundState struct {
	Height    int64
	Round     int32
	Step      int
	StartTime time.Time
	// Rounds 0..Round+1 of the current height: CometBFT already tracks the
	// next round (to collect early votes), so the last entry is usually
	// empty. Use Current() for the round in progress.
	Rounds   []RoundVotes
	Proposer struct {
		Address string `json:"address"`
		Index   int    `json:"index"`
	}
}

// RoundVotes holds one round's votes, indexed like the validator set.
type RoundVotes struct {
	Round      int32
	Prevotes   []Vote
	Precommits []Vote
}

// Vote is a parsed entry of a vote list. CometBFT renders votes as strings;
// "nil-Vote" means no vote has been received from that validator.
type Vote struct {
	Received      bool
	AddressPrefix string // first 6 bytes of the validator address, hex
	ForNil        bool   // voted, but for no block
}

// Current returns the votes of the round in progress.
func (rs *RoundState) Current() RoundVotes {
	for _, r := range rs.Rounds {
		if r.Round == rs.Round {
			return r
		}
	}
	return RoundVotes{Round: rs.Round}
}

// Step names, from consensus/types/round_state.go.
var stepNames = map[int]string{
	1: "NewHeight", 2: "NewRound", 3: "Propose", 4: "Prevote",
	5: "PrevoteWait", 6: "Precommit", 7: "PrecommitWait", 8: "Commit",
}

func StepName(step int) string {
	if n, ok := stepNames[step]; ok {
		return n
	}
	return strconv.Itoa(step)
}

func (c *Client) ConsensusState(ctx context.Context) (*RoundState, error) {
	var out struct {
		RoundState struct {
			HeightRoundStep string    `json:"height/round/step"`
			StartTime       time.Time `json:"start_time"`
			Votes           []struct {
				Round      int32    `json:"round"`
				Prevotes   []string `json:"prevotes"`
				Precommits []string `json:"precommits"`
			} `json:"height_vote_set"`
			Proposer struct {
				Address string `json:"address"`
				Index   int    `json:"index"`
			} `json:"proposer"`
		} `json:"round_state"`
	}
	if err := c.call(ctx, safehttp.CometConsensus, nil, &out); err != nil {
		return nil, err
	}
	rs := out.RoundState
	var h int64
	var r int32
	var st int
	if _, err := fmt.Sscanf(rs.HeightRoundStep, "%d/%d/%d", &h, &r, &st); err != nil {
		return nil, &Error{Code: CodeBadResponse, Detail: fmt.Sprintf("unexpected height/round/step %q", rs.HeightRoundStep)}
	}
	state := &RoundState{Height: h, Round: r, Step: st, StartTime: rs.StartTime}
	state.Proposer.Address = rs.Proposer.Address
	state.Proposer.Index = rs.Proposer.Index
	for _, v := range rs.Votes {
		state.Rounds = append(state.Rounds, RoundVotes{Round: v.Round, Prevotes: parseVotes(v.Prevotes), Precommits: parseVotes(v.Precommits)})
	}
	return state, nil
}

// voteRe matches Vote.String():
// Vote{<index>:<addr 6B> <height>/<round>/<type>(<Type>) <block hash 6B> <sig 6B> <ext 6B> @ <time>}
var voteRe = regexp.MustCompile(`^Vote\{\d+:([0-9A-F]{12}) \d+/\d+/\S+ ([0-9A-F]{12}) `)

const nilBlockHash = "000000000000"

func parseVotes(in []string) []Vote {
	out := make([]Vote, len(in))
	for i, s := range in {
		m := voteRe.FindStringSubmatch(s)
		if m == nil {
			continue // "nil-Vote", or a format we don't know: treat as not received
		}
		out[i] = Vote{Received: true, AddressPrefix: m[1], ForNil: m[2] == nilBlockHash}
	}
	return out
}

// MatchesAddress reports whether a full hex address has this vote's prefix.
func (v Vote) MatchesAddress(addr string) bool {
	return v.Received && strings.HasPrefix(strings.ToUpper(addr), v.AddressPrefix)
}
