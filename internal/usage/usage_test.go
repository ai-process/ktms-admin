package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildAggregatesAndJudges(t *testing.T) {
	rows := []Row{
		{Bucket: "2026-10-01T00:00:00Z", Model: "big", ProjectID: "hog", Status: "success", TokensInput: 900000, TokensOutput: 100000, TokensTotal: 1000000, RequestCount: 100, SumDurationMs: 100000},
		{Bucket: "2026-10-02T00:00:00Z", Model: "big", ProjectID: "hog", Status: "error", TokensInput: 100000, TokensOutput: 0, TokensTotal: 100000, RequestCount: 10, SumDurationMs: 1000},
		{Bucket: "2026-10-01T00:00:00Z", Model: "small", ProjectID: "lean", Status: "success", TokensInput: 20000, TokensOutput: 10000, TokensTotal: 30000, RequestCount: 300, SumDurationMs: 30000},
		{Bucket: "2026-10-02T00:00:00Z", Model: "small", ProjectID: "lean", Status: "success", TokensInput: 20000, TokensOutput: 10000, TokensTotal: 30000, RequestCount: 300, SumDurationMs: 30000},
	}
	prices := map[string]Price{"big": {In: 10, Out: 30}, "small": {In: 1, Out: 2}}
	rep := Build(rows, "project", prices)

	if rep.Totals.TokensTotal != 1160000 || rep.Totals.Requests != 710 || rep.Totals.Failed != 10 {
		t.Fatalf("totals: %+v", rep.Totals)
	}
	if len(rep.Groups) != 2 || rep.Groups[0].Key != "hog" {
		t.Fatalf("groups not sorted by tokens: %+v", rep.Groups)
	}
	hog, lean := rep.Groups[0], rep.Groups[1]
	if hog.Verdict != "greedy" || lean.Verdict != "worthy" {
		t.Errorf("verdicts: hog=%q lean=%q", hog.Verdict, lean.Verdict)
	}
	if hog.FailedTokens != 100000 || hog.Failed != 10 {
		t.Errorf("hog failures: %+v", hog)
	}
	// hog: 900k in x $10/M + 100k out x $30/M = 12, plus the failed 100k in = 1 -> 13
	if hog.CostUSD == nil || *hog.CostUSD < 12.99 || *hog.CostUSD > 13.01 {
		t.Errorf("hog cost: %v", hog.CostUSD)
	}
	if !rep.CostKnown || len(rep.Daily) != 2 || rep.Daily[0].Bucket != "2026-10-01" {
		t.Errorf("daily/cost: %+v", rep.Daily)
	}
}

func TestBuildWithoutPricesHasNoCost(t *testing.T) {
	rep := Build([]Row{{Bucket: "2026-10-01T00:00:00Z", Model: "x", Status: "success", TokensTotal: 10, RequestCount: 1}}, "none", nil)
	if rep.CostKnown || rep.Totals.CostUSD != nil || rep.Groups[0].Key != "total" {
		t.Errorf("unexpected: %+v", rep)
	}
}

func TestSingleGroupHasNoVerdict(t *testing.T) {
	rep := Build([]Row{{Bucket: "2026-10-01T00:00:00Z", ProjectID: "only", Status: "success", TokensTotal: 100, RequestCount: 50}}, "project", nil)
	if rep.Groups[0].Verdict != "" {
		t.Errorf("a lone group cannot be greedy or worthy")
	}
}

// fakeTokenmeter answers like light-tokenmeter with auth on: only "r" may read.
func fakeTokenmeter(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer r":
		case "Bearer w":
			w.WriteHeader(http.StatusForbidden)
			return
		default:
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("from") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"rows":[{"model":"m","tokens_total":7}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTokenIsSentAndRefusalsAreNamed(t *testing.T) {
	base := fakeTokenmeter(t)
	ctx := context.Background()
	now := time.Now()

	ok := NewClient(base, "r")
	if err := ok.Ready(ctx); err != nil {
		t.Fatalf("Ready with a read token: %v", err)
	}
	rows, err := ok.Summary(ctx, now.AddDate(0, 0, -1), now, "none")
	if err != nil || len(rows) != 1 || rows[0].TokensTotal != 7 {
		t.Fatalf("Summary with a read token: %v, %+v", err, rows)
	}

	for token, want := range map[string]string{
		"":    "set TOKENMETER_TOKEN",
		"bad": "TOKENMETER_TOKEN was rejected",
		"w":   "lacks the read scope",
	} {
		c := NewClient(base, token)
		if err := c.Ready(ctx); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Ready with token %q: %v, want %q", token, err, want)
		}
		if _, err := c.Summary(ctx, now.AddDate(0, 0, -1), now, "none"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Summary with token %q: %v, want %q", token, err, want)
		}
	}
}
