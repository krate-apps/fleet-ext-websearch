package main

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// searchHit is a normalized ExtSearch result (PD-XS-01).
type searchHit struct {
	Engine     string `json:"engine"`
	Title      string `json:"title,omitempty"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	SizeLabel  string `json:"sizeLabel,omitempty"`
	Magnet     string `json:"magnet,omitempty"`
	TorrentURL string `json:"torrentUrl,omitempty"`
	Page       string `json:"page,omitempty"`
	InfoHash   string `json:"infoHash,omitempty"`
	Kind       string `json:"kind"` // magnet | torrent
	ParseDiag  string `json:"parseDiag,omitempty"`
}

type engineDiag struct {
	Engine    string `json:"engine"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
	Hits      int    `json:"hits"`
	ParseNote string `json:"parseNote,omitempty"`
}

var (
	sizeNearRE = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(KiB|MiB|GiB|TiB|KB|MB|GB|TB|Ko|Mo|Go|To)\b`)
)

func normalizeMagnetHit(engine, page, magnet string) searchHit {
	h := searchHit{
		Engine:    engine,
		Magnet:    magnet,
		Page:      page,
		Kind:      "magnet",
		InfoHash:  magnetInfoHash(magnet),
		Title:     magnetDisplayName(magnet),
		SizeBytes: magnetSizeBytes(magnet),
	}
	if h.SizeBytes > 0 {
		h.SizeLabel = formatSize(h.SizeBytes)
	}
	notes := make([]string, 0, 3)
	if h.Title == "" {
		notes = append(notes, "no dn=")
		if h.InfoHash != "" {
			h.Title = h.InfoHash
		} else {
			h.Title = "magnet"
		}
	}
	if h.InfoHash == "" {
		notes = append(notes, "no btih")
	}
	if h.SizeBytes == 0 {
		notes = append(notes, "no xl=")
	}
	h.ParseDiag = strings.Join(notes, "; ")
	return h
}

func normalizeTorrentHit(engine, page, torrentURL string) searchHit {
	h := searchHit{
		Engine:     engine,
		TorrentURL: torrentURL,
		Page:       page,
		Kind:       "torrent",
		Title:      torrentTitleFromURL(torrentURL),
		ParseDiag:  "size unknown (url only)",
	}
	if h.Title == "" {
		h.Title = torrentURL
		h.ParseDiag = "no filename; size unknown"
	}
	return h
}

func magnetDisplayName(magnet string) string {
	u, err := url.Parse(magnet)
	if err != nil {
		return fallbackDN(magnet)
	}
	dn := u.Query().Get("dn")
	if dn == "" {
		return fallbackDN(magnet)
	}
	return strings.TrimSpace(dn)
}

func fallbackDN(magnet string) string {
	lower := strings.ToLower(magnet)
	const marker = "dn="
	i := strings.Index(lower, marker)
	if i < 0 {
		return ""
	}
	rest := magnet[i+len(marker):]
	end := strings.IndexAny(rest, "&")
	if end >= 0 {
		rest = rest[:end]
	}
	decoded, err := url.QueryUnescape(rest)
	if err != nil {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(decoded)
}

func magnetSizeBytes(magnet string) int64 {
	u, err := url.Parse(magnet)
	if err == nil {
		if xl := strings.TrimSpace(u.Query().Get("xl")); xl != "" {
			n, err := strconv.ParseInt(xl, 10, 64)
			if err == nil && n > 0 {
				return n
			}
		}
	}
	lower := strings.ToLower(magnet)
	const marker = "xl="
	i := strings.Index(lower, marker)
	if i < 0 {
		return 0
	}
	rest := magnet[i+len(marker):]
	end := strings.IndexAny(rest, "&")
	if end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func torrentTitleFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	base = strings.TrimSuffix(base, path.Ext(base))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	if decoded, err := url.QueryUnescape(base); err == nil {
		return decoded
	}
	return base
}

func formatSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i < 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	if v >= 10 {
		return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
	}
	return strconv.FormatFloat(v, 'f', 2, 64) + " " + units[i]
}

// parseTorrentsCSVBody handles the torrents-csv JSON API when present.
func parseTorrentsCSVBody(body, page, engine string) ([]searchHit, string) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || trimmed[0] != '{' && trimmed[0] != '[' {
		return nil, "not json"
	}
	var wrap struct {
		Torrents []struct {
			Name      string  `json:"name"`
			Infohash  string  `json:"infohash"`
			SizeBytes int64   `json:"size_bytes"`
			Size      float64 `json:"size"`
			Magnet    string  `json:"magnet"`
		} `json:"torrents"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrap); err != nil {
		// try bare array
		var rows []struct {
			Name      string `json:"name"`
			Infohash  string `json:"infohash"`
			SizeBytes int64  `json:"size_bytes"`
			Magnet    string `json:"magnet"`
		}
		if err2 := json.Unmarshal([]byte(trimmed), &rows); err2 != nil {
			return nil, "json parse failed"
		}
		wrap.Torrents = make([]struct {
			Name      string  `json:"name"`
			Infohash  string  `json:"infohash"`
			SizeBytes int64   `json:"size_bytes"`
			Size      float64 `json:"size"`
			Magnet    string  `json:"magnet"`
		}, len(rows))
		for i, r := range rows {
			wrap.Torrents[i].Name = r.Name
			wrap.Torrents[i].Infohash = r.Infohash
			wrap.Torrents[i].SizeBytes = r.SizeBytes
			wrap.Torrents[i].Magnet = r.Magnet
		}
	}
	if len(wrap.Torrents) == 0 {
		return nil, "json ok, 0 torrents"
	}
	out := make([]searchHit, 0, len(wrap.Torrents))
	for _, row := range wrap.Torrents {
		mag := strings.TrimSpace(row.Magnet)
		hash := strings.ToUpper(strings.TrimSpace(row.Infohash))
		if mag == "" && hash != "" {
			mag = "magnet:?xt=urn:btih:" + hash
		}
		if mag == "" {
			continue
		}
		h := normalizeMagnetHit(engine, page, mag)
		if title := strings.TrimSpace(row.Name); title != "" {
			h.Title = title
		}
		if row.SizeBytes > 0 {
			h.SizeBytes = row.SizeBytes
			h.SizeLabel = formatSize(row.SizeBytes)
		} else if row.Size > 0 {
			h.SizeBytes = int64(row.Size)
			h.SizeLabel = formatSize(h.SizeBytes)
		}
		if hash != "" {
			h.InfoHash = hash
		}
		h.ParseDiag = "torrents-csv json"
		out = append(out, h)
	}
	return out, "torrents-csv json"
}

func enrichSizeFromNearbyHTML(body, magnetOrURL string, hit *searchHit) {
	if hit == nil || hit.SizeBytes > 0 {
		return
	}
	idx := strings.Index(body, magnetOrURL)
	if idx < 0 {
		// try truncated magnet
		if hit.Magnet != "" && len(hit.Magnet) > 40 {
			idx = strings.Index(body, hit.Magnet[:40])
		}
	}
	if idx < 0 {
		return
	}
	start := idx - 120
	if start < 0 {
		start = 0
	}
	end := idx + len(magnetOrURL) + 120
	if end > len(body) {
		end = len(body)
	}
	window := body[start:end]
	m := sizeNearRE.FindStringSubmatch(window)
	if len(m) < 3 {
		return
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 {
		return
	}
	unit := strings.ToLower(m[2])
	mult := float64(1)
	switch unit {
	case "kib", "kb", "ko":
		mult = 1024
	case "mib", "mb", "mo":
		mult = 1024 * 1024
	case "gib", "gb", "go":
		mult = 1024 * 1024 * 1024
	case "tib", "tb", "to":
		mult = 1024 * 1024 * 1024 * 1024
	}
	hit.SizeBytes = int64(v * mult)
	hit.SizeLabel = formatSize(hit.SizeBytes)
	if hit.ParseDiag == "" {
		hit.ParseDiag = "size from nearby html"
	} else if !strings.Contains(hit.ParseDiag, "size") {
		hit.ParseDiag += "; size from nearby html"
	}
}

func sortHits(hits []searchHit, sortBy string) []searchHit {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	if sortBy == "" || sortBy == "engine" {
		return hits
	}
	out := append([]searchHit(nil), hits...)
	switch sortBy {
	case "size", "size-desc":
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[j].SizeBytes > out[i].SizeBytes {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
	case "size-asc":
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[j].SizeBytes < out[i].SizeBytes && out[j].SizeBytes > 0 || out[i].SizeBytes == 0 && out[j].SizeBytes > 0 {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
	case "title":
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if strings.ToLower(out[j].Title) < strings.ToLower(out[i].Title) {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
	}
	return out
}

func filterHits(hits []searchHit, engine, kind string) []searchHit {
	engine = strings.TrimSpace(engine)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if engine == "" && kind == "" {
		return hits
	}
	out := make([]searchHit, 0, len(hits))
	for _, h := range hits {
		if engine != "" && !strings.EqualFold(h.Engine, engine) {
			continue
		}
		if kind != "" && h.Kind != kind {
			continue
		}
		out = append(out, h)
	}
	return out
}
