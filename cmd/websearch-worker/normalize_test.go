package main

import "testing"

func TestNormalizeMagnetHit_success(t *testing.T) {
	mag := "magnet:?xt=urn:btih:952CF2E0BC39F1208DCC31830A7EB2BED5B02BFB&dn=My%20Title&xl=1048576"
	h := normalizeMagnetHit("Nyaa", "https://nyaa.si/?q=x", mag)
	if h.Title != "My Title" {
		t.Fatalf("title=%q", h.Title)
	}
	if h.SizeBytes != 1048576 || h.SizeLabel == "" {
		t.Fatalf("size=%d %q", h.SizeBytes, h.SizeLabel)
	}
	if h.InfoHash != "952CF2E0BC39F1208DCC31830A7EB2BED5B02BFB" {
		t.Fatalf("hash=%q", h.InfoHash)
	}
	if h.Kind != "magnet" {
		t.Fatalf("kind=%q", h.Kind)
	}
}

func TestNormalizeMagnetHit_failureMissingMeta(t *testing.T) {
	h := normalizeMagnetHit("X", "https://x/", "magnet:?xt=urn:btih:short")
	if h.ParseDiag == "" {
		t.Fatal("expected parseDiag")
	}
	if h.InfoHash != "" {
		t.Fatalf("hash=%q", h.InfoHash)
	}
}

func TestNormalizeTorrentHit_titleFromPath(t *testing.T) {
	h := normalizeTorrentHit("E", "https://e/", "https://cdn.example/files/Cool.Show.S01E01.torrent")
	if h.Title != "Cool.Show.S01E01" {
		t.Fatalf("title=%q", h.Title)
	}
	if h.Kind != "torrent" {
		t.Fatalf("kind=%q", h.Kind)
	}
}

func TestParseTorrentsCSVBody_success(t *testing.T) {
	body := `{"torrents":[{"name":"Alpha","infohash":"952CF2E0BC39F1208DCC31830A7EB2BED5B02BFB","size_bytes":2048,"magnet":"magnet:?xt=urn:btih:952CF2E0BC39F1208DCC31830A7EB2BED5B02BFB&dn=Alpha"}]}`
	hits, note := parseTorrentsCSVBody(body, "https://torrents-csv.com/x", "TorrentsCSV")
	if note != "torrents-csv json" || len(hits) != 1 {
		t.Fatalf("%q %#v", note, hits)
	}
	if hits[0].Title != "Alpha" || hits[0].SizeBytes != 2048 {
		t.Fatalf("%#v", hits[0])
	}
}

func TestParseTorrentsCSVBody_failureNotJSON(t *testing.T) {
	hits, note := parseTorrentsCSVBody("<html>nope</html>", "https://x", "TorrentsCSV")
	if len(hits) != 0 || note != "not json" {
		t.Fatalf("%q %#v", note, hits)
	}
}

func TestSortAndFilterHits(t *testing.T) {
	hits := []searchHit{
		{Engine: "A", Title: "b", SizeBytes: 10, Kind: "magnet"},
		{Engine: "B", Title: "a", SizeBytes: 100, Kind: "torrent"},
	}
	sorted := sortHits(hits, "size")
	if sorted[0].SizeBytes != 100 {
		t.Fatalf("%#v", sorted)
	}
	filtered := filterHits(hits, "B", "torrent")
	if len(filtered) != 1 || filtered[0].Engine != "B" {
		t.Fatalf("%#v", filtered)
	}
}

func TestEnrichSizeFromNearbyHTML(t *testing.T) {
	mag := "magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	body := `<td>1.5 GiB</td><a href="` + mag + `">x</a>`
	h := normalizeMagnetHit("E", "https://e/", mag)
	h.SizeBytes = 0
	h.SizeLabel = ""
	enrichSizeFromNearbyHTML(body, mag, &h)
	if h.SizeBytes < 1024*1024*1024 {
		t.Fatalf("size=%d", h.SizeBytes)
	}
}
