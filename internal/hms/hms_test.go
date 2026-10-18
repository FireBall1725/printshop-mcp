// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package hms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// The fixtures are real responses from Bambu's public lookup.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseIntro(t *testing.T) {
	got := ParseIntro(fixture(t, "lookup_ok.json"))
	if !strings.HasPrefix(got, "Not enough space on MicroSD Card.") {
		t.Fatalf("%q", got)
	}
	if ParseIntro(fixture(t, "lookup_unknown.json")) != "" {
		t.Fatal("an unknown code must give no text")
	}
	printErr := `{"result":0,"data":{"device_error":{"en":[{"ecode":"03004000","intro":"Z axis homing failed; the task has been stopped."}]}}}`
	if ParseIntro([]byte(printErr)) != "Z axis homing failed; the task has been stopped." {
		t.Fatal("print errors come back under device_error")
	}
	for _, bad := range []string{"", "not json", `{"result":0,"data":{}}`, `{"result":0,"data":{"device_hms":{"en":[]}}}`} {
		if ParseIntro([]byte(bad)) != "" {
			t.Errorf("%q should give nothing", bad)
		}
	}
}

func serve(t *testing.T, hits *atomic.Int32, codes *[]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		*codes = append(*codes, r.URL.Query().Get("e"))
		if r.URL.Query().Get("e") == "0500010000030004" {
			_, _ = w.Write(fixture(t, "lookup_ok.json"))
			return
		}
		_, _ = w.Write(fixture(t, "lookup_unknown.json"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDescribeCachesAndOnlySendsHex(t *testing.T) {
	var hits atomic.Int32
	var codes []string
	l := New(serve(t, &hits, &codes).URL)
	ctx := context.Background()

	got := l.Describe(ctx, []string{"0500010000030004"})
	if !strings.Contains(got["0500010000030004"], "MicroSD") {
		t.Fatalf("%v", got)
	}
	l.Describe(ctx, []string{"0500010000030004"})
	if hits.Load() != 1 {
		t.Fatalf("a found code must be cached, got %d requests", hits.Load())
	}

	// An unknown code is asked about once, then remembered as unknown.
	l.Describe(ctx, []string{"FFFFFFFFFFFFFFFF"})
	l.Describe(ctx, []string{"FFFFFFFFFFFFFFFF"})
	if hits.Load() != 2 {
		t.Fatalf("a miss must not be retried at once, got %d requests", hits.Load())
	}

	before := hits.Load()
	for _, bad := range []string{"", "../../etc/passwd", "0500 0100", "TESTSERIAL012345", "0500010000030004&x=1", "ZZZZZZZZ"} {
		if got := l.Describe(ctx, []string{bad}); len(got) != 0 {
			t.Errorf("%q returned %v", bad, got)
		}
	}
	if hits.Load() != before {
		t.Fatalf("something that is not a hex code was sent upstream: %v", codes)
	}
}

func TestDescribeCapsHowManyCodesItAsks(t *testing.T) {
	var hits atomic.Int32
	var codes []string
	l := New(serve(t, &hits, &codes).URL)
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, strings.Repeat("A", 8)+strings.Repeat("0", 7)+string("0123456789ABCDEF"[i%16]))
	}
	l.Describe(context.Background(), many)
	if hits.Load() > maxCodes {
		t.Fatalf("asked %d times", hits.Load())
	}
}

func TestDisabledAndNilSendNothing(t *testing.T) {
	var hits atomic.Int32
	var codes []string
	srv := serve(t, &hits, &codes)
	_ = srv
	if got := New("").Describe(context.Background(), []string{"0500010000030004"}); len(got) != 0 {
		t.Fatal("a disabled lookup returned text")
	}
	var nilL *Lookup
	if got := nilL.Describe(context.Background(), []string{"0500010000030004"}); len(got) != 0 {
		t.Fatal("a nil lookup returned text")
	}
	if hits.Load() != 0 {
		t.Fatal("a request was made")
	}
}

func TestUpstreamFailureIsQuiet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) }))
	defer srv.Close()
	if got := New(srv.URL).Describe(context.Background(), []string{"0500010000030004"}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	if got := New(url).Describe(context.Background(), []string{"0500010000030004"}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
