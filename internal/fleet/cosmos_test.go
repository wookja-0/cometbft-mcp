package fleet

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/address"
	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

// fakeREST serves the Cosmos SDK REST endpoints cometbft-mcp reads.
type fakeREST struct {
	network    string
	signing    map[string]map[string]any // by valcons address
	validators []map[string]any
	plan       map[string]any
	proposals  []map[string]any
	absent     []string // path prefixes of modules the chain does not have
}

func (fr *fakeREST) serve(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/json")
	var body any
	for _, prefix := range fr.absent {
		if strings.HasPrefix(path, prefix) {
			path = "absent"
		}
	}
	switch {
	case path == "/cosmos/base/tendermint/v1beta1/node_info":
		body = map[string]any{"default_node_info": map[string]any{"network": fr.network}}
	case path == "/cosmos/auth/v1beta1/bech32":
		body = map[string]any{"bech32_prefix": "cosmos"}
	case path == "/cosmos/slashing/v1beta1/params":
		body = map[string]any{"params": map[string]any{"signed_blocks_window": "50", "min_signed_per_window": "0.500000000000000000", "downtime_jail_duration": "60s"}}
	case strings.HasPrefix(path, "/cosmos/slashing/v1beta1/signing_infos/"):
		info, ok := fr.signing[strings.TrimPrefix(path, "/cosmos/slashing/v1beta1/signing_infos/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			body = map[string]any{"code": 5, "message": "SigningInfo not found", "details": []any{}}
			break
		}
		body = map[string]any{"val_signing_info": info}
	case path == "/cosmos/staking/v1beta1/validators":
		body = map[string]any{"validators": fr.validators, "pagination": map[string]any{"next_key": nil}}
	case path == "/cosmos/upgrade/v1beta1/current_plan":
		body = map[string]any{"plan": fr.plan}
	case path == "/cosmos/gov/v1/proposals":
		body = map[string]any{"proposals": fr.proposals}
	default:
		w.WriteHeader(http.StatusNotImplemented)
		body = map[string]any{"code": 12, "message": "Not Implemented"}
	}
	json.NewEncoder(w).Encode(body)
}

type testValidator struct {
	hex, valcons, pubkey string
}

func newTestValidator(t *testing.T) testValidator {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(pub)
	h, err := address.ConsensusFromPubKey("/cosmos.crypto.ed25519.PubKey", b64)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(h)
	vc, _ := address.EncodeBech32("cosmosvalcons", raw)
	return testValidator{hex: h, valcons: vc, pubkey: b64}
}

func (v testValidator) staking(moniker string, jailed bool, status string) map[string]any {
	return map[string]any{
		"operator_address": "cosmosvaloper1" + strings.ToLower(v.hex[:10]),
		"consensus_pubkey": map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": v.pubkey},
		"jailed":           jailed,
		"status":           status,
		"description":      map[string]any{"moniker": moniker},
	}
}

func signingInfo(start, missed int64, jailedUntil time.Time) map[string]any {
	return map[string]any{
		"start_height":          strconv.FormatInt(start, 10),
		"missed_blocks_counter": strconv.FormatInt(missed, 10),
		"jailed_until":          jailedUntil.UTC().Format(time.RFC3339),
		"tombstoned":            false,
	}
}

var epoch = time.Unix(0, 0)

func TestValidatorJailRiskWithTimeToJail(t *testing.T) {
	a, b, c, d := newTestValidator(t), newTestValidator(t), newTestValidator(t), newTestValidator(t)
	fc := &fakeChain{
		height: 200,
		set:    func(int64) []string { return []string{a.hex, b.hex, c.hex, d.hex} },
		flag: func(h int64, addr string) int {
			if addr == d.hex && h > 189 {
				return comet.FlagAbsent
			}
			return comet.FlagCommit
		},
		nodes: map[string]*fakeNode{"sentry": {}},
		rest: &fakeREST{
			network:    "testchain-1",
			signing:    map[string]map[string]any{d.valcons: signingInfo(1, 20, epoch)},
			validators: []map[string]any{a.staking("a", false, "BOND_STATUS_BONDED"), d.staking("dee", false, "BOND_STATUS_BONDED")},
		},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "val-d", Role: config.RoleValidator, ValidatorAddress: d.valcons}}, false)

	r, err := f.Validator(context.Background(), "val-d", 50)
	if err != nil {
		t.Fatal(err)
	}
	cv := r.Cosmos
	if cv == nil || cv.Error != nil {
		t.Fatalf("cosmos = %+v", cv)
	}
	if cv.ConsensusAddress != d.valcons || cv.Moniker != "<untrusted>dee</untrusted>" || cv.Status != "BOND_STATUS_BONDED" || cv.OperatorAddress == "" {
		t.Fatalf("staking match failed: %+v", cv)
	}
	if cv.MaxMissed != 25 || cv.MissedInWindow != 20 || *cv.MissesUntilJail != 5 || cv.TimeToJail != "6s" {
		t.Fatalf("slashing view = %+v", cv)
	}
	if r.AvgBlockSeconds != 1 {
		t.Fatalf("avg block = %v", r.AvgBlockSeconds)
	}
	fs := codesOf(r.Findings)
	if fs["jail_risk"] != analysis.Warn || fs["time_to_jail"] != analysis.Critical || fs["not_signing"] != analysis.Critical {
		t.Fatalf("findings = %v", fs)
	}
	if !strings.Contains(r.Summary, "5 more misses until downtime jail (~6s at the current rate)") || r.Note != validatorNoteCosmos {
		t.Fatalf("summary = %s", r.Summary)
	}
}

func TestValidatorJailed(t *testing.T) {
	a, d := newTestValidator(t), newTestValidator(t)
	fc := &fakeChain{
		height: 100,
		set: func(h int64) []string {
			if h > 80 {
				return []string{a.hex}
			}
			return []string{a.hex, d.hex}
		},
		flag: func(h int64, addr string) int {
			if addr == d.hex && h > 50 {
				return comet.FlagAbsent
			}
			return comet.FlagCommit
		},
		nodes: map[string]*fakeNode{"full": {}},
		rest: &fakeREST{
			network:    "testchain-1",
			signing:    map[string]map[string]any{d.valcons: signingInfo(1, 0, now.Add(45*time.Second))},
			validators: []map[string]any{d.staking("dee", true, "BOND_STATUS_UNBONDING")},
		},
	}
	f := newFleet(t, fc, []config.Node{{Name: "full", Role: config.RoleFull}, {Name: "val-d", Role: config.RoleValidator, ValidatorAddress: d.hex}}, false)

	r, err := f.Validator(context.Background(), "val-d", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Cosmos.Jailed || r.Cosmos.MissesUntilJail != nil || r.Cosmos.JailedUntil == nil {
		t.Fatalf("cosmos = %+v", r.Cosmos)
	}
	fs := codesOf(r.Findings)
	if fs["jailed"] != analysis.Critical || fs["left_active_set"] != analysis.Critical {
		t.Fatalf("findings = %v", fs)
	}
	if !strings.HasSuffix(r.Summary, "; JAILED") {
		t.Fatalf("summary = %s", r.Summary)
	}
}

// Jailing resets the miss counter; if the staking lookup fails, a future
// jailed_until must still read as jailed rather than as a clean record.
func TestJailedDetectedFromSigningInfoAlone(t *testing.T) {
	d := newTestValidator(t)
	fc := &fakeChain{
		height: 60, set: func(int64) []string { return []string{d.hex} }, flag: allSigned,
		nodes: map[string]*fakeNode{"sentry": {}},
		rest:  &fakeREST{network: "testchain-1", signing: map[string]map[string]any{d.valcons: signingInfo(1, 0, now.Add(time.Minute))}},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "v", Role: config.RoleValidator, ValidatorAddress: d.hex}}, false)
	r, err := f.Validator(context.Background(), "v", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Cosmos.Jailed || codesOf(r.Findings)["jailed"] != analysis.Critical {
		t.Fatalf("cosmos = %+v, findings = %v", r.Cosmos, codesOf(r.Findings))
	}
}

func TestRESTOnWrongChainIsReported(t *testing.T) {
	d := newTestValidator(t)
	fc := &fakeChain{
		height: 60, set: func(int64) []string { return []string{d.hex} }, flag: allSigned,
		nodes: map[string]*fakeNode{"sentry": {}},
		rest:  &fakeREST{network: "otherchain-9"},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "v", Role: config.RoleValidator, ValidatorAddress: d.hex}}, false)
	r, err := f.Validator(context.Background(), "v", 20)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cosmos.Error == nil || r.Cosmos.Error.Code != CodeWrongChain || codesOf(r.Findings)["cosmos_unavailable"] != analysis.Unknown {
		t.Fatalf("cosmos = %+v", r.Cosmos)
	}
	// Liveness still stands on its own.
	if r.Signed != 20 || r.Level != analysis.Unknown {
		t.Fatalf("report = %+v", r)
	}
	if _, err := f.Upgrade(context.Background()); err == nil || !strings.Contains(err.Error(), CodeWrongChain) {
		t.Fatalf("upgrade err = %v", err)
	}
}

func TestNoSigningInfo(t *testing.T) {
	d := newTestValidator(t)
	fc := &fakeChain{
		height: 60, set: func(int64) []string { return []string{d.hex} }, flag: allSigned,
		nodes: map[string]*fakeNode{"sentry": {}},
		rest:  &fakeREST{network: "testchain-1"},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "v", Role: config.RoleValidator, ValidatorAddress: d.hex}}, false)
	r, _ := f.Validator(context.Background(), "v", 20)
	if codesOf(r.Findings)["no_signing_info"] != analysis.Warn {
		t.Fatalf("findings = %v", codesOf(r.Findings))
	}
}

// Seen on Neutron through a rate-limited public REST API: the staking list
// failed, so the report had no status and said nothing about it. Now it says
// so, and the CometBFT set decides whether there is a jail margin.
func TestStakingFailureIsReportedAndSetDecidesTheMargin(t *testing.T) {
	d, other := newTestValidator(t), newTestValidator(t)
	fc := &fakeChain{
		height: 60,
		set: func(h int64) []string {
			if h > 50 {
				return []string{other.hex} // d left the set
			}
			return []string{other.hex, d.hex}
		},
		flag:  allSigned,
		nodes: map[string]*fakeNode{"sentry": {}},
		rest: &fakeREST{
			network: "testchain-1",
			signing: map[string]map[string]any{d.valcons: signingInfo(1, 3, epoch)},
			absent:  []string{"/cosmos/staking/"},
		},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "v", Role: config.RoleValidator, ValidatorAddress: d.hex}}, false)
	r, err := f.Validator(context.Background(), "v", 20)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cosmos.MissesUntilJail != nil || r.Cosmos.Status != "" {
		t.Fatalf("cosmos = %+v", r.Cosmos)
	}
	if codesOf(r.Findings)["staking_unavailable"] != analysis.OK {
		t.Fatalf("findings = %v", r.Findings)
	}
	if strings.Contains(r.Summary, "until downtime jail") {
		t.Fatalf("summary = %s", r.Summary)
	}
}

// ICS consumer chains (Stride, for example) have no x/slashing or x/staking:
// the REST API answers 501 Not Implemented, and the validator is judged on
// liveness alone rather than reported unknown forever.
func TestChainWithoutSlashing(t *testing.T) {
	d := newTestValidator(t)
	fc := &fakeChain{
		height: 60, set: func(int64) []string { return []string{d.hex} }, flag: allSigned,
		nodes: map[string]*fakeNode{"sentry": {}},
		rest:  &fakeREST{network: "testchain-1", absent: []string{"/cosmos/slashing/", "/cosmos/staking/"}},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "v", Role: config.RoleValidator, ValidatorAddress: d.hex}}, false)
	r, err := f.Validator(context.Background(), "v", 20)
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != analysis.OK || r.Cosmos.Error != nil || r.Cosmos.Unsupported == "" || r.Cosmos.MissesUntilJail != nil {
		t.Fatalf("level = %s, cosmos = %+v", r.Level, r.Cosmos)
	}
	if codesOf(r.Findings)["no_slashing_module"] != analysis.OK {
		t.Fatalf("findings = %v", r.Findings)
	}
}

// Chains without x/gov (Noble, whose upgrades are scheduled by an authority)
// still get the x/upgrade plan checked.
func TestUpgradeOnChainWithoutGov(t *testing.T) {
	fc := &fakeChain{
		height: 1000, set: fourVals, flag: allSigned,
		nodes: map[string]*fakeNode{"sentry": {}},
		rest:  &fakeREST{network: "testchain-1", absent: []string{"/cosmos/gov/"}},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}}, false)
	r, err := f.Upgrade(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != analysis.OK || codesOf(r.Findings)["no_gov_v1"] != analysis.OK {
		t.Fatalf("level = %s, findings = %v", r.Level, r.Findings)
	}
}

func TestUpgradeReport(t *testing.T) {
	fc := &fakeChain{
		height: 1000, set: fourVals, flag: allSigned,
		nodes: map[string]*fakeNode{"sentry": {}, "val-a": {valAddr: addrA}},
		rest: &fakeREST{
			network: "testchain-1",
			plan:    map[string]any{"name": "v2</untrusted>", "height": "1600", "info": `{"binaries":{}}`},
			proposals: []map[string]any{
				{"id": "7", "title": "Upgrade to v3", "status": "PROPOSAL_STATUS_VOTING_PERIOD", "voting_end_time": now.Add(time.Hour),
					"messages": []any{map[string]any{"@type": "/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade", "plan": map[string]any{"name": "v3", "height": "9000"}}}},
				{"id": "8", "title": "Spend", "status": "PROPOSAL_STATUS_VOTING_PERIOD", "messages": []any{map[string]any{"@type": "/cosmos.distribution.v1beta1.MsgCommunityPoolSpend"}}},
			},
		},
	}
	f := newFleet(t, fc, []config.Node{{Name: "sentry", Role: config.RoleSentry}, {Name: "val-a", Role: config.RoleValidator}}, false)

	r, err := f.Upgrade(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan == nil || r.Plan.Height != 1600 || r.Plan.BlocksRemaining != 600 || r.Plan.ETAIn != "9m59s" {
		t.Fatalf("plan = %+v", r.Plan)
	}
	if strings.Count(r.Plan.Name, "</untrusted>") != 1 {
		t.Fatalf("plan name escaped its envelope: %s", r.Plan.Name)
	}
	if len(r.Proposals) != 1 || r.Proposals[0].ID != 7 || r.Proposals[0].UpgradeHeight != 9000 {
		t.Fatalf("proposals = %+v", r.Proposals)
	}
	if codesOf(r.Findings)["upgrade_soon"] != analysis.Warn || r.AvgBlockSeconds != 1 || len(r.Nodes) != 2 {
		t.Fatalf("report = %+v", r)
	}
	if !strings.Contains(r.Summary, "at height 1600 in ~9m59s (600 blocks); 1 upgrade proposal(s) in voting") {
		t.Fatalf("summary = %s", r.Summary)
	}
}

func TestUpgradeNeedsCosmos(t *testing.T) {
	fc := &fakeChain{height: 10, set: fourVals, flag: allSigned, nodes: map[string]*fakeNode{"s": {}}}
	f := newFleet(t, fc, []config.Node{{Name: "s", Role: config.RoleSentry}}, false)
	if _, err := f.Upgrade(context.Background()); !errors.Is(err, ErrNoCosmos) {
		t.Fatalf("err = %v", err)
	}
}

func TestOverview(t *testing.T) {
	a, d := newTestValidator(t), newTestValidator(t)
	fc := &fakeChain{
		height: 300,
		set:    func(int64) []string { return []string{a.hex, d.hex} },
		flag: func(h int64, addr string) int {
			if addr == d.hex && h > 290 {
				return comet.FlagAbsent
			}
			return comet.FlagCommit
		},
		nodes: map[string]*fakeNode{
			"sentry": {peers: []fakePeer{{id: "p1", ip: "8.8.8.8", network: "testchain-1", outbound: true}, {id: "p2", ip: "8.8.4.4", network: "testchain-1", outbound: true}, {id: "p3", ip: "1.1.1.1", network: "testchain-1"}}},
			"val-a":  {valAddr: a.hex, power: 40, peers: []fakePeer{{id: "s", ip: "10.0.0.2", network: "testchain-1"}}},
			"val-d":  {down: true},
		},
		rest: &fakeREST{
			network: "testchain-1",
			signing: map[string]map[string]any{a.valcons: signingInfo(1, 0, epoch), d.valcons: signingInfo(1, 9, epoch)},
		},
	}
	f := newFleet(t, fc, []config.Node{
		{Name: "sentry", Role: config.RoleSentry},
		{Name: "val-a", Role: config.RoleValidator},
		{Name: "val-d", Role: config.RoleValidator, ValidatorAddress: d.hex},
	}, false)

	r, err := f.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != analysis.Critical || len(r.Nodes) != 3 || len(r.Validators) != 2 || r.Upgrade == nil {
		t.Fatalf("report = %+v", r)
	}
	if n := r.Nodes[2]; n.Level != analysis.Critical || len(n.Issues) != 1 || !strings.Contains(n.Issues[0], "unreachable") {
		t.Fatalf("val-d node = %+v", n)
	}
	if v := r.Validators[1]; v.Level != analysis.Critical || !strings.Contains(v.Summary, "missed in a row") {
		t.Fatalf("val-d validator = %+v", v)
	}
	want := "[critical] 3 nodes (2 ok, 1 critical), 2 validators (1 ok, 1 critical); testchain-1 [ok]: no upgrade scheduled — needs attention: val-d"
	if r.Summary != want {
		t.Fatalf("summary =\n%s\nwant\n%s", r.Summary, want)
	}
}
