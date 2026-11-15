package main

import (
	"strings"
	"testing"
)

func TestExtractMagnets(t *testing.T) {
	body := `<a href="magnet:?xt=urn:btih:ABC&amp;dn=one">x</a> magnet:?xt=urn:btih:DEF dn=two`
	got := extractMagnets(body)
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if got[0] != "magnet:?xt=urn:btih:ABC&dn=one" {
		t.Fatalf("first=%q", got[0])
	}
}

func TestExtractMagnetsHrefQuoted(t *testing.T) {
	body := `<a href='magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=q'>q</a>`
	got := extractMagnets(body)
	if len(got) != 1 {
		t.Fatalf("got %#v", got)
	}
	if !strings.Contains(got[0], "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("unexpected %q", got[0])
	}
}

func TestExtractTorrentURLs(t *testing.T) {
	page := "https://example.test/search?q=x"
	body := `<a href="/dl/foo.torrent">t</a> also https://cdn.example.test/bar.torrent?token=1`
	got := extractTorrentURLs(body, page)
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if got[0] != "https://example.test/dl/foo.torrent" {
		t.Fatalf("first=%q", got[0])
	}
	if got[1] != "https://cdn.example.test/bar.torrent?token=1" {
		t.Fatalf("second=%q", got[1])
	}
}

func TestValidateEngines(t *testing.T) {
	if err := validateEngines(nil); err == nil {
		t.Fatal("expected error")
	}
	if err := validateEngines([]engine{{Name: "X", URL: "https://ex.ample/?q={query}"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateEngines([]engine{{Name: "X", URL: "https://ex.ample/?q=nope"}}); err == nil {
		t.Fatal("expected {query} error")
	}
}

func TestParseEnginesTextInterval(t *testing.T) {
	got, err := parseEnginesText("Mine|https://ex.ample/?q={query}|1500\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MinIntervalMs != 1500 || got[0].Name != "Mine" {
		t.Fatalf("%#v", got)
	}
	if _, err := parseEnginesText("Mine|https://ex.ample/?q={query}|100"); err == nil {
		t.Fatal("expected min interval error")
	}
	if _, err := parseEnginesText("Nyaa|https://ex.ample/?q={query}"); err == nil {
		t.Fatal("expected packaged name collision")
	}
}

func TestMergeEnginesSkipsUserCollision(t *testing.T) {
	user := []engine{{Name: "nyaa", URL: "https://evil.test/?q={query}", MinIntervalMs: 2000}}
	merged := mergeEngines(packagedEngines(), user)
	for _, e := range merged {
		if !e.Packaged && strings.EqualFold(e.Name, "nyaa") {
			t.Fatal("user engine should not override packaged")
		}
	}
	if len(merged) != 3 {
		t.Fatalf("len=%d", len(merged))
	}
}

func TestMagnetInfoHash(t *testing.T) {
	h := magnetInfoHash("magnet:?xt=urn:btih:952CF2E0BC39F1208DCC31830A7EB2BED5B02BFB&dn=x")
	if h != "952CF2E0BC39F1208DCC31830A7EB2BED5B02BFB" {
		t.Fatalf("got %q", h)
	}
	if magnetInfoHash("magnet:?xt=urn:btih:short") != "" {
		t.Fatal("expected empty")
	}
}

func TestEngineInterval(t *testing.T) {
	if engineInterval(engine{}) != defaultMinIntervalMs {
		t.Fatal("default")
	}
	if engineInterval(engine{MinIntervalMs: 100}) != minIntervalMsFloor {
		t.Fatal("floor")
	}
	if engineInterval(engine{MinIntervalMs: 3000}) != 3000 {
		t.Fatal("custom")
	}
}
