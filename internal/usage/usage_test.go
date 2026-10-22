package usage

import (
	"context"
	"errors"
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

// fakeTokenmeter answers like light-tokenmeter: with auth on only "r" may
// read; a request without from/to fails validation after auth.
func fakeTokenmeter(t *testing.T, authOn bool) string {
	refuse := func(w http.ResponseWriter, status int, code string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"no","code":"` + code + `"}`))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/readyz":
			return
		case r.URL.Path == "/proxy-says-no":
			w.WriteHeader(http.StatusForbidden) // a WAF in front: no light-tokenmeter body
			return
		case !authOn:
		case r.Header.Get("Authorization") == "Bearer r":
		case r.Header.Get("Authorization") == "Bearer w":
			refuse(w, http.StatusForbidden, "forbidden")
			return
		default:
			refuse(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		if r.URL.Query().Get("from") == "" {
			refuse(w, http.StatusBadRequest, "validation_error")
			return
		}
		_, _ = w.Write([]byte(`{"rows":[{"model":"m","tokens_total":7}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTokenIsSentAndRefusalsAreNamed(t *testing.T) {
	base := fakeTokenmeter(t, true)
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
		var authErr *AuthError
		if err := c.Ready(ctx); !errors.As(err, &authErr) || !strings.Contains(err.Error(), want) {
			t.Errorf("Ready with token %q: %v, want an AuthError with %q", token, err, want)
		}
		if _, err := c.Summary(ctx, now.AddDate(0, 0, -1), now, "none"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Summary with token %q: %v, want %q", token, err, want)
		}
	}
}

func TestAuthOffNeedsNoToken(t *testing.T) {
	c := NewClient(fakeTokenmeter(t, false), "")
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("Ready without auth: %v", err)
	}
}

func TestOtherRefusalsDoNotBlameTheToken(t *testing.T) {
	c := NewClient(fakeTokenmeter(t, true), "r")
	resp, err := c.get(context.Background(), "/proxy-says-no")
	if err != nil {
		t.Fatal(err)
	}
	err = c.check(resp.StatusCode, read(resp, 1<<10))
	var authErr *AuthError
	if err == nil || errors.As(err, &authErr) || strings.Contains(err.Error(), "TOKENMETER_TOKEN") {
		t.Fatalf("a 403 without light-tokenmeter's code blamed the token: %v", err)
	}
	if err := c.check(http.StatusInternalServerError, []byte(`{"error":"Failed to get summary","code":"internal_error"}`)); err == nil || !strings.Contains(err.Error(), "Failed to get summary") {
		t.Fatalf("light-tokenmeter's message was dropped: %v", err)
	}
}
