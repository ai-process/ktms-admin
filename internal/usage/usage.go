// Package usage talks to light-tokenmeter and turns its daily rows into the
// per-dimension view the console shows.
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Dimensions the console can group by; "none" gives totals only.
var Dimensions = map[string]string{
	"project": "project_id", "user": "user_id", "model": "model",
	"action": "action_id", "vendor": "vendor", "none": "",
}

// Row is one light-tokenmeter summary row.
type Row struct {
	Bucket        string `json:"bucket"`
	ProjectID     string `json:"project_id"`
	UserID        string `json:"user_id"`
	Vendor        string `json:"vendor"`
	Model         string `json:"model"`
	ActionID      string `json:"action_id"`
	Status        string `json:"status"`
	TokensInput   int64  `json:"tokens_input"`
	TokensOutput  int64  `json:"tokens_output"`
	TokensTotal   int64  `json:"tokens_total"`
	RequestCount  int64  `json:"request_count"`
	SumDurationMs int64  `json:"sum_duration_ms"`
}

type Client struct {
	base string
	http *http.Client
}

func NewClient(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 30 * time.Second}}
}

// Ready checks light-tokenmeter's readiness probe.
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/readyz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("light-tokenmeter not ready: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Summary fetches rows between from (inclusive) and to (exclusive), split by
// day, model and status, plus the chosen dimension.
func (c *Client) Summary(ctx context.Context, from, to time.Time, dim string) ([]Row, error) {
	col, ok := Dimensions[dim]
	if !ok {
		return nil, fmt.Errorf("unknown group %q", dim)
	}
	q := url.Values{}
	q.Set("from", from.UTC().Format(time.RFC3339))
	q.Set("to", to.UTC().Format(time.RFC3339))
	q.Set("group_by_model", "true")
	q.Set("group_by_status", "true")
	if col != "" {
		q.Set("group_by_"+col, "true")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/usage/summary?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("light-tokenmeter returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Rows []Row `json:"rows"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}
	return out.Rows, nil
}

// Price is a model's list price in USD per million tokens.
type Price struct{ In, Out float64 }

// Group is the aggregate for one value of the chosen dimension.
type Group struct {
	Key          string   `json:"key"`
	TokensInput  int64    `json:"tokens_input"`
	TokensOutput int64    `json:"tokens_output"`
	TokensTotal  int64    `json:"tokens_total"`
	Requests     int64    `json:"requests"`
	Failed       int64    `json:"failed"`
	FailedTokens int64    `json:"failed_tokens"`
	AvgLatencyMs float64  `json:"avg_latency_ms"`
	TokensPerReq float64  `json:"tokens_per_request"`
	FailureRate  float64  `json:"failure_rate"`
	TokenShare   float64  `json:"token_share"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
	Verdict      string   `json:"verdict"`
	durationMs   int64
}

type Day struct {
	Bucket   string   `json:"bucket"`
	Tokens   int64    `json:"tokens"`
	Requests int64    `json:"requests"`
	Failed   int64    `json:"failed"`
	CostUSD  *float64 `json:"cost_usd,omitempty"`
}

type Report struct {
	Totals Group   `json:"totals"`
	Groups []Group `json:"groups"`
	Daily  []Day   `json:"daily"`
	// CostKnown is true when prices were available for at least one model seen.
	CostKnown bool `json:"cost_known"`
}

func dimValue(r Row, dim string) string {
	var v string
	switch dim {
	case "project":
		v = r.ProjectID
	case "user":
		v = r.UserID
	case "model":
		v = r.Model
	case "action":
		v = r.ActionID
	case "vendor":
		v = r.Vendor
	default:
		return "total"
	}
	if v == "" {
		return "(none)"
	}
	return v
}

func failed(status string) bool { return status != "success" }

// Build aggregates rows by dimension. prices may be nil (usage-only setup).
func Build(rows []Row, dim string, prices map[string]Price) *Report {
	groups := map[string]*Group{}
	days := map[string]*Day{}
	rep := &Report{}
	var total Group

	add := func(g *Group, r Row, cost float64, priced bool) {
		g.TokensInput += r.TokensInput
		g.TokensOutput += r.TokensOutput
		g.TokensTotal += r.TokensTotal
		g.Requests += r.RequestCount
		g.durationMs += r.SumDurationMs
		if failed(r.Status) {
			g.Failed += r.RequestCount
			g.FailedTokens += r.TokensTotal
		}
		if priced {
			if g.CostUSD == nil {
				z := 0.0
				g.CostUSD = &z
			}
			*g.CostUSD += cost
		}
	}
	for _, r := range rows {
		var cost float64
		p, priced := prices[r.Model]
		if priced {
			cost = float64(r.TokensInput)*p.In/1e6 + float64(r.TokensOutput)*p.Out/1e6
			rep.CostKnown = true
		}
		key := dimValue(r, dim)
		g := groups[key]
		if g == nil {
			g = &Group{Key: key}
			groups[key] = g
		}
		add(g, r, cost, priced)
		add(&total, r, cost, priced)

		day := r.Bucket
		if len(day) >= 10 {
			day = day[:10]
		}
		d := days[day]
		if d == nil {
			d = &Day{Bucket: day}
			days[day] = d
		}
		d.Tokens += r.TokensTotal
		d.Requests += r.RequestCount
		if failed(r.Status) {
			d.Failed += r.RequestCount
		}
		if priced {
			if d.CostUSD == nil {
				z := 0.0
				d.CostUSD = &z
			}
			*d.CostUSD += cost
		}
	}

	finish := func(g *Group) {
		if g.Requests > 0 {
			g.AvgLatencyMs = float64(g.durationMs) / float64(g.Requests)
			g.TokensPerReq = float64(g.TokensTotal) / float64(g.Requests)
			g.FailureRate = float64(g.Failed) / float64(g.Requests)
		}
		if total.TokensTotal > 0 {
			g.TokenShare = float64(g.TokensTotal) / float64(total.TokensTotal)
		}
	}
	finish(&total)
	total.Key = "total"
	rep.Totals = total
	for _, g := range groups {
		finish(g)
		g.Verdict = verdict(g, &total, len(groups))
		rep.Groups = append(rep.Groups, *g)
	}
	sort.Slice(rep.Groups, func(i, j int) bool {
		if rep.Groups[i].TokensTotal != rep.Groups[j].TokensTotal {
			return rep.Groups[i].TokensTotal > rep.Groups[j].TokensTotal
		}
		return rep.Groups[i].Key < rep.Groups[j].Key
	})
	for _, d := range days {
		rep.Daily = append(rep.Daily, *d)
	}
	sort.Slice(rep.Daily, func(i, j int) bool { return rep.Daily[i].Bucket < rep.Daily[j].Bucket })
	return rep
}

// verdict labels a group "greedy" (a big slice of all tokens at a
// per-request cost well above average, or a lot of tokens wasted on failures)
// or "worthy" (a real share, lean per request, reliable). Neutral otherwise.
func verdict(g, total *Group, n int) string {
	if n < 2 || total.Requests == 0 || g.Requests < 5 {
		return ""
	}
	avg := total.TokensPerReq
	wasted := 0.0
	if g.TokensTotal > 0 {
		wasted = float64(g.FailedTokens) / float64(g.TokensTotal)
	}
	switch {
	case g.TokenShare >= 0.10 && g.TokensPerReq >= 1.5*avg,
		g.TokenShare >= 0.05 && wasted >= 0.20:
		return "greedy"
	case g.TokenShare >= 0.01 && g.TokensPerReq <= avg && g.FailureRate <= 0.05:
		return "worthy"
	}
	return ""
}
