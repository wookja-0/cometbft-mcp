// Package fleet gathers data from the configured nodes and produces the
// reports the MCP tools return. Every string a node controls is wrapped as
// untrusted, and network identifiers are masked unless revealing is enabled.
package fleet

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wookja-0/cometbft-mcp/internal/analysis"
	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/sanitize"
	"github.com/wookja-0/cometbft-mcp/internal/source/comet"
)

type Fleet struct {
	cfg        *config.Config
	clients    map[string]*comet.Client
	net        sanitize.Network
	thresholds analysis.Thresholds
	now        func() time.Time
}

type Options struct {
	RevealNetwork bool
	Thresholds    analysis.Thresholds // zero value = defaults
	Now           func() time.Time    // for tests
}

func New(cfg *config.Config, h *safehttp.Client, opts Options) *Fleet {
	f := &Fleet{
		cfg:        cfg,
		clients:    map[string]*comet.Client{},
		net:        sanitize.Network{Reveal: opts.RevealNetwork},
		thresholds: opts.Thresholds,
		now:        opts.Now,
	}
	if f.thresholds == (analysis.Thresholds{}) {
		f.thresholds = analysis.DefaultThresholds
	}
	if f.now == nil {
		f.now = time.Now
	}
	for _, n := range cfg.Nodes {
		f.clients[n.Name] = comet.NewClient(h, n.RPCURL)
	}
	return f
}

// NodeError is a failure that is safe to show the model.
type NodeError struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (f *Fleet) nodeError(err error) *NodeError {
	var se *safehttp.Error
	if errors.As(err, &se) {
		ne := &NodeError{Code: se.Code, Detail: se.Detail}
		if f.net.Reveal && se.Raw() != nil {
			ne.Detail = se.Raw().Error()
		}
		return ne
	}
	var ce *comet.Error
	if errors.As(err, &ce) {
		return &NodeError{Code: ce.Code, Detail: ce.Detail}
	}
	return &NodeError{Code: "internal_error", Detail: err.Error()}
}

func (e *NodeError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// selectNodes resolves an optional node name to the nodes to inspect.
func (f *Fleet) selectNodes(name string) ([]config.Node, error) {
	if name == "" {
		return f.cfg.Nodes, nil
	}
	n, ok := f.cfg.Node(name)
	if !ok {
		return nil, fmt.Errorf("unknown node %q; configured nodes: %s", name, strings.Join(f.cfg.Names(), ", "))
	}
	return []config.Node{*n}, nil
}

// each runs fn for every node concurrently and returns results in input order.
func each[T any](nodes []config.Node, fn func(config.Node) T) []T {
	out := make([]T, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = fn(n)
		}()
	}
	wg.Wait()
	return out
}

var chainIDRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// chainID passes through well-formed chain IDs and wraps anything else.
func chainID(s string) string {
	if chainIDRe.MatchString(s) {
		return s
	}
	return sanitize.Untrusted(s)
}

// summarize builds a one-line fleet summary such as
// "5 nodes: 3 ok, 1 warn, 1 critical — val3: rpc_unreachable".
func summarize(kind string, names []string, levels []analysis.Level, firstIssue []string) string {
	counts := map[analysis.Level]int{}
	for _, l := range levels {
		counts[l]++
	}
	var parts []string
	for _, l := range []analysis.Level{analysis.OK, analysis.Unknown, analysis.Warn, analysis.Critical} {
		if counts[l] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[l], l))
		}
	}
	if len(names) == 1 {
		kind = strings.TrimSuffix(kind, "s")
	}
	s := fmt.Sprintf("%d %s: %s", len(names), kind, strings.Join(parts, ", "))

	type issue struct {
		name  string
		level analysis.Level
		msg   string
	}
	var issues []issue
	for i, l := range levels {
		if l != analysis.OK && firstIssue[i] != "" {
			issues = append(issues, issue{names[i], l, firstIssue[i]})
		}
	}
	sort.SliceStable(issues, func(a, b int) bool { return issues[a].level.MoreSevere(issues[b].level) })
	if len(issues) > 0 {
		var is []string
		for _, i := range issues {
			is = append(is, i.name+": "+i.msg)
		}
		s += " — " + strings.Join(is, "; ")
	}
	return s
}

func (r *StatusReport) GetSummary() string    { return r.Summary }
func (r *PeerReport) GetSummary() string      { return r.Summary }
func (r *ValidatorReport) GetSummary() string { return r.Summary }

func firstMessage(fs []analysis.Finding, err *NodeError) string {
	if err != nil {
		return err.Code
	}
	worst := analysis.LevelOf(fs)
	for _, f := range fs {
		if f.Level == worst {
			return f.Code
		}
	}
	return ""
}

func durationString(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Second).String()
	}
}
