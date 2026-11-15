// Command websearch-worker searches configured torrent sites and extracts magnets (ruTorrent extsearch parity).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMinIntervalMs = 2000
	minIntervalMsFloor   = 500
	maxHits              = 100
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

type engine struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	MinIntervalMs int    `json:"minIntervalMs,omitempty"`
	Packaged      bool   `json:"packaged,omitempty"`
}

type settings struct {
	UserEngines []engine         `json:"userEngines"`
	Engines     []engine         `json:"engines,omitempty"` // legacy 0.1
	LastFetchAt map[string]int64 `json:"lastFetchAt,omitempty"`
	GatewayID   string           `json:"gatewayId,omitempty"`
	Label       string           `json:"label,omitempty"`
}

var (
	mu       sync.Mutex
	nextHost = int64(1)
	pending  = map[int64]chan hostReply{}
	outMu    sync.Mutex

	hrefTorrentRE = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+\.torrent(?:\?[^"']*)?)["']`)
	bareTorrentRE = regexp.MustCompile(`(?i)https?://[^\s"'<>]+\.torrent(?:\?[^\s"'<>]*)?`)
	hrefMagnetRE  = regexp.MustCompile(`(?i)href\s*=\s*["'](magnet:\?[^"']+)["']`)
)

type hostReply struct {
	result json.RawMessage
	err    string
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for in.Scan() {
		line := append([]byte(nil), in.Bytes()...)
		if len(line) == 0 {
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if _, hasMethod := probe["method"]; !hasMethod {
			var resp struct {
				ID     any             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(line, &resp); err != nil {
				continue
			}
			idNum, ok := asInt64(resp.ID)
			if !ok {
				continue
			}
			mu.Lock()
			ch := pending[idNum]
			delete(pending, idNum)
			mu.Unlock()
			if ch == nil {
				continue
			}
			msg := ""
			if resp.Error != nil {
				msg = resp.Error.Message
			}
			ch <- hostReply{result: resp.Result, err: msg}
			continue
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		// Concurrent so hostCall reverse-RPC can complete while stdin keeps reading replies.
		go handleRequest(req)
	}
}

func handleRequest(req request) {
	switch req.Method {
	case "fleet.handshake", "fleet.ping", "ext.activate", "ext.deactivate", "ext.health", "ext.upgrade":
		reply(req.ID, map[string]any{"ok": true})
	case "fleet.shutdown":
		reply(req.ID, map[string]any{"ok": true})
		os.Exit(0)
	case "ext.websearch.getSettings":
		st, err := loadSettings()
		if err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		packaged := packagedEngines()
		reply(req.ID, settingsView(st, packaged))
	case "ext.websearch.saveSettings":
		var p struct {
			EnginesText string `json:"enginesText"`
			GatewayID   string `json:"gatewayId"`
			Label       string `json:"label"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			replyErr(req.ID, "invalid settings")
			return
		}
		st, err := loadSettings()
		if err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		parsed, err := parseEnginesText(p.EnginesText)
		if err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		if err := validateUserEngines(parsed); err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		st.UserEngines = parsed
		st.GatewayID = strings.TrimSpace(p.GatewayID)
		st.Label = strings.TrimSpace(p.Label)
		st.Engines = nil
		if err := saveSettings(&st); err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		reply(req.ID, settingsView(st, packagedEngines()))
	case "ext.websearch.resetDefaults":
		st, err := loadSettings()
		if err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		packagedNames := packagedNameSet()
		kept := make([]engine, 0, len(st.UserEngines))
		for _, e := range st.UserEngines {
			if _, hit := packagedNames[strings.ToLower(e.Name)]; hit {
				continue
			}
			kept = append(kept, e)
		}
		st.UserEngines = kept
		st.Engines = nil
		if st.LastFetchAt != nil {
			for name := range packagedNames {
				delete(st.LastFetchAt, name)
				// also delete exact-cased packaged names
			}
			for _, pe := range packagedEngines() {
				delete(st.LastFetchAt, pe.Name)
				delete(st.LastFetchAt, strings.ToLower(pe.Name))
			}
		}
		if err := saveSettings(&st); err != nil {
			replyErr(req.ID, err.Error())
			return
		}
		reply(req.ID, settingsView(st, packagedEngines()))
	case "ext.websearch.search":
		handleSearch(req.ID, req.Params)
	case "ext.websearch.enqueue":
		handleEnqueue(req.ID, req.Params)
	default:
		replyErr(req.ID, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func handleSearch(id any, params json.RawMessage) {
	var p struct {
		Query     string `json:"query"`
		Engine    string `json:"engine"`
		Add       bool   `json:"add"`
		DryRun    bool   `json:"dryRun"`
		GatewayID string `json:"gatewayId"`
		Label     string `json:"label"`
		SortBy    string `json:"sortBy"`    // engine|size|size-asc|title
		FilterKind string `json:"filterKind"` // magnet|torrent|""
	}
	if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		replyErr(id, "query required")
		return
	}
	st, err := loadSettings()
	if err != nil {
		replyErr(id, err.Error())
		return
	}
	q := strings.TrimSpace(p.Query)
	want := strings.TrimSpace(p.Engine)
	all := mergeEngines(packagedEngines(), st.UserEngines)
	selected := make([]engine, 0)
	for _, e := range all {
		if want != "" && !strings.EqualFold(e.Name, want) {
			continue
		}
		selected = append(selected, e)
	}
	if len(selected) == 0 {
		replyErr(id, "no matching engine")
		return
	}
	now := time.Now().UnixMilli()
	if st.LastFetchAt == nil {
		st.LastFetchAt = map[string]int64{}
	}
	for _, e := range selected {
		interval := engineInterval(e)
		last := st.LastFetchAt[e.Name]
		if last == 0 {
			last = st.LastFetchAt[strings.ToLower(e.Name)]
		}
		if last > 0 {
			elapsed := now - last
			if elapsed < int64(interval) {
				remain := int64(interval) - elapsed
				replyErr(id, fmt.Sprintf("rate limit: engine %q available in %dms", e.Name, remain))
				return
			}
		}
	}

	links := make([]map[string]string, 0)
	hits := make([]searchHit, 0)
	diags := make([]engineDiag, 0, len(selected))
	seenMag := map[string]struct{}{}
	seenTor := map[string]struct{}{}
	for _, e := range selected {
		pageURL := strings.ReplaceAll(e.URL, "{query}", url.QueryEscape(q))
		links = append(links, map[string]string{"name": e.Name, "url": pageURL})
		diag := engineDiag{Engine: e.Name}
		body, status, err := httpFetch(pageURL)
		st.LastFetchAt[e.Name] = time.Now().UnixMilli()
		diag.Status = status
		if err != nil {
			diag.Error = err.Error()
			diags = append(diags, diag)
			continue
		}
		if status >= 400 {
			diag.Error = fmt.Sprintf("http %d", status)
			diags = append(diags, diag)
			continue
		}
		before := len(hits)
		if strings.EqualFold(e.Name, "TorrentsCSV") {
			csvHits, note := parseTorrentsCSVBody(body, pageURL, e.Name)
			diag.ParseNote = note
			for _, h := range csvHits {
				key := strings.ToLower(h.Magnet)
				if _, ok := seenMag[key]; ok {
					continue
				}
				seenMag[key] = struct{}{}
				hits = append(hits, h)
				if len(hits) >= maxHits {
					break
				}
			}
		}
		if len(hits) < maxHits {
			for _, mag := range extractMagnets(body) {
				key := strings.ToLower(mag)
				if _, ok := seenMag[key]; ok {
					continue
				}
				seenMag[key] = struct{}{}
				h := normalizeMagnetHit(e.Name, pageURL, mag)
				enrichSizeFromNearbyHTML(body, mag, &h)
				hits = append(hits, h)
				if len(hits) >= maxHits {
					break
				}
			}
		}
		if len(hits) < maxHits {
			for _, tor := range extractTorrentURLs(body, pageURL) {
				key := strings.ToLower(tor)
				if _, ok := seenTor[key]; ok {
					continue
				}
				seenTor[key] = struct{}{}
				h := normalizeTorrentHit(e.Name, pageURL, tor)
				enrichSizeFromNearbyHTML(body, tor, &h)
				hits = append(hits, h)
				if len(hits) >= maxHits {
					break
				}
			}
		}
		diag.Hits = len(hits) - before
		if diag.ParseNote == "" {
			if diag.Hits == 0 {
				diag.ParseNote = "no magnets/torrents extracted"
			} else {
				diag.ParseNote = "html extract"
			}
		}
		diags = append(diags, diag)
		if len(hits) >= maxHits {
			break
		}
	}
	_ = saveSettings(&st)

	gatewayID := strings.TrimSpace(p.GatewayID)
	if gatewayID == "" {
		gatewayID = strings.TrimSpace(st.GatewayID)
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		label = strings.TrimSpace(st.Label)
	}

	hits = filterHits(hits, "", p.FilterKind)
	hits = sortHits(hits, p.SortBy)

	added := 0
	dryNotice := ""
	if p.Add && len(hits) > 0 {
		if p.DryRun {
			dryNotice = fmt.Sprintf("Dry-run: would enqueue %d hit(s) via %s", len(hits), intakeTarget(gatewayID))
			if label != "" {
				dryNotice += " with label " + label
			}
		} else {
			n, err := enqueueHits(hits, gatewayID, label)
			if err != nil {
				replyErr(id, err.Error())
				return
			}
			added = n
		}
	}
	reply(id, map[string]any{
		"query":        q,
		"links":        links,
		"results":      hits,
		"resultCount":  len(hits),
		"added":        added,
		"dryRun":       p.DryRun && p.Add,
		"dryNotice":    dryNotice,
		"gatewayId":    gatewayID,
		"label":        label,
		"diagnostics":  diags,
		"sortBy":       p.SortBy,
		"filterKind":   p.FilterKind,
	})
}

func handleEnqueue(id any, params json.RawMessage) {
	var p struct {
		Magnet     string `json:"magnet"`
		TorrentURL string `json:"torrentUrl"`
		Engine     string `json:"engine"`
		Title      string `json:"title"`
		GatewayID  string `json:"gatewayId"`
		Label      string `json:"label"`
		DryRun     bool   `json:"dryRun"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		replyErr(id, "invalid enqueue params")
		return
	}
	mag := strings.TrimSpace(p.Magnet)
	tor := strings.TrimSpace(p.TorrentURL)
	if mag == "" && tor == "" {
		replyErr(id, "magnet or torrentUrl required")
		return
	}
	st, err := loadSettings()
	if err != nil {
		replyErr(id, err.Error())
		return
	}
	gatewayID := strings.TrimSpace(p.GatewayID)
	if gatewayID == "" {
		gatewayID = strings.TrimSpace(st.GatewayID)
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		label = strings.TrimSpace(st.Label)
	}
	hit := searchHit{
		Engine:     strings.TrimSpace(p.Engine),
		Title:      strings.TrimSpace(p.Title),
		Magnet:     mag,
		TorrentURL: tor,
	}
	if mag != "" {
		hit = normalizeMagnetHit(hit.Engine, "", mag)
		if t := strings.TrimSpace(p.Title); t != "" {
			hit.Title = t
		}
	} else {
		hit = normalizeTorrentHit(hit.Engine, "", tor)
		if t := strings.TrimSpace(p.Title); t != "" {
			hit.Title = t
		}
	}
	target := intakeTarget(gatewayID)
	notice := fmt.Sprintf("Would enqueue %q (%s) via %s", hit.Title, hit.Kind, target)
	if label != "" {
		notice += " with label " + label
	}
	if p.DryRun {
		reply(id, map[string]any{
			"ok":        true,
			"dryRun":    true,
			"dryNotice": notice,
			"hit":       hit,
			"gatewayId": gatewayID,
			"label":     label,
			"added":     0,
		})
		return
	}
	n, err := enqueueHits([]searchHit{hit}, gatewayID, label)
	if err != nil {
		replyErr(id, err.Error())
		return
	}
	reply(id, map[string]any{
		"ok":        true,
		"dryRun":    false,
		"dryNotice": "",
		"hit":       hit,
		"gatewayId": gatewayID,
		"label":     label,
		"added":     n,
		"notice":    fmt.Sprintf("Enqueued %d via %s", n, target),
	})
}

func intakeTarget(gatewayID string) string {
	if strings.TrimSpace(gatewayID) == "" {
		return "default client"
	}
	return "gateway " + gatewayID
}

func settingsView(st settings, packaged []engine) map[string]any {
	return map[string]any{
		"packagedEngines":     packaged,
		"packagedEnginesText": enginesText(packaged),
		"engines":             st.UserEngines,
		"enginesText":         enginesText(st.UserEngines),
		"gatewayId":           st.GatewayID,
		"label":               st.Label,
		"lastFetchAt":         st.LastFetchAt,
		"help":                "Packaged engines are built-in (read-only). User engines: Name|URL|minIntervalMs (interval optional, default 2000, min 500). URL must include {query}.",
	}
}

func packagedEngines() []engine {
	return []engine{
		{Name: "Nyaa", URL: "https://nyaa.si/?f=0&c=0_0&q={query}&s=seeders&o=desc", MinIntervalMs: defaultMinIntervalMs, Packaged: true},
		{Name: "SolidTorrents", URL: "https://solidtorrents.to/search?q={query}", MinIntervalMs: defaultMinIntervalMs, Packaged: true},
		{Name: "TorrentsCSV", URL: "https://torrents-csv.com/service/search?q={query}&size=50", MinIntervalMs: defaultMinIntervalMs, Packaged: true},
	}
}

func packagedNameSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, e := range packagedEngines() {
		out[strings.ToLower(e.Name)] = struct{}{}
	}
	return out
}

func mergeEngines(packaged, user []engine) []engine {
	out := make([]engine, 0, len(packaged)+len(user))
	out = append(out, packaged...)
	seen := packagedNameSet()
	for _, e := range user {
		key := strings.ToLower(e.Name)
		if _, hit := seen[key]; hit {
			continue
		}
		seen[key] = struct{}{}
		e.Packaged = false
		out = append(out, e)
	}
	return out
}

func engineInterval(e engine) int {
	if e.MinIntervalMs < minIntervalMsFloor {
		if e.MinIntervalMs <= 0 {
			return defaultMinIntervalMs
		}
		return minIntervalMsFloor
	}
	return e.MinIntervalMs
}

func enginesText(engines []engine) string {
	lines := make([]string, 0, len(engines))
	for _, e := range engines {
		interval := e.MinIntervalMs
		if interval <= 0 {
			interval = defaultMinIntervalMs
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%d", e.Name, e.URL, interval))
	}
	return strings.Join(lines, "\n")
}

func parseEnginesText(text string) ([]engine, error) {
	out := make([]engine, 0)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			return nil, fmt.Errorf("expected Name|URL[|minIntervalMs] on line %q", line)
		}
		name := strings.TrimSpace(parts[0])
		rawURL := strings.TrimSpace(parts[1])
		if name == "" || rawURL == "" {
			return nil, fmt.Errorf("empty name or url on line %q", line)
		}
		interval := defaultMinIntervalMs
		if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
			n, err := strconv.Atoi(strings.TrimSpace(parts[2]))
			if err != nil {
				return nil, fmt.Errorf("invalid minIntervalMs on line %q", line)
			}
			interval = n
		}
		if interval < minIntervalMsFloor {
			return nil, fmt.Errorf("minIntervalMs must be >= %d on line %q", minIntervalMsFloor, line)
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate engine name %q", name)
		}
		if _, packaged := packagedNameSet()[key]; packaged {
			return nil, fmt.Errorf("engine name %q collides with packaged engine", name)
		}
		seen[key] = struct{}{}
		out = append(out, engine{Name: name, URL: rawURL, MinIntervalMs: interval})
	}
	return out, nil
}

func validateUserEngines(engines []engine) error {
	if len(engines) > 50 {
		return fmt.Errorf("too many engines (max 50)")
	}
	packaged := packagedNameSet()
	for _, e := range engines {
		if strings.TrimSpace(e.Name) == "" {
			return fmt.Errorf("engine name required")
		}
		if _, hit := packaged[strings.ToLower(e.Name)]; hit {
			return fmt.Errorf("engine name %q collides with packaged engine", e.Name)
		}
		u := strings.TrimSpace(e.URL)
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return fmt.Errorf("engine %q url must be http(s)", e.Name)
		}
		if !strings.Contains(u, "{query}") {
			return fmt.Errorf("engine %q url must contain {query}", e.Name)
		}
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("engine %q has invalid url", e.Name)
		}
		if e.MinIntervalMs > 0 && e.MinIntervalMs < minIntervalMsFloor {
			return fmt.Errorf("engine %q minIntervalMs must be >= %d", e.Name, minIntervalMsFloor)
		}
	}
	return nil
}

// validateEngines keeps legacy test helper for a single engine list (user-style rules).
func validateEngines(engines []engine) error {
	if len(engines) == 0 {
		return fmt.Errorf("at least one engine required")
	}
	return validateUserEngines(engines)
}

func extractMagnets(body string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, "&amp;", "&")
		s = strings.ReplaceAll(s, "&#38;", "&")
		if !strings.HasPrefix(strings.ToLower(s), "magnet:?") {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, m := range hrefMagnetRE.FindAllStringSubmatch(body, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	lower := strings.ToLower(body)
	src := body
	for {
		i := strings.Index(lower, "magnet:?")
		if i < 0 {
			break
		}
		rest := src[i:]
		end := strings.IndexAny(rest, "\"'<> \n\r\t")
		if end < 0 {
			add(rest)
			break
		}
		add(rest[:end])
		src = src[i+end:]
		lower = strings.ToLower(src)
	}
	return out
}

func extractTorrentURLs(body, pageURL string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.ReplaceAll(raw, "&amp;", "&")
		if raw == "" {
			return
		}
		abs := resolveURL(pageURL, raw)
		if abs == "" {
			return
		}
		lower := strings.ToLower(abs)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			return
		}
		if !strings.Contains(lower, ".torrent") {
			return
		}
		if _, ok := seen[lower]; ok {
			return
		}
		seen[lower] = struct{}{}
		out = append(out, abs)
	}
	for _, m := range hrefTorrentRE.FindAllStringSubmatch(body, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	for _, m := range bareTorrentRE.FindAllString(body, -1) {
		add(m)
	}
	return out
}

func resolveURL(page, href string) string {
	base, err := url.Parse(page)
	if err != nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func magnetInfoHash(magnet string) string {
	lower := strings.ToLower(magnet)
	const marker = "xt=urn:btih:"
	i := strings.Index(lower, marker)
	if i < 0 {
		return ""
	}
	rest := magnet[i+len(marker):]
	end := strings.IndexAny(rest, "&")
	if end >= 0 {
		rest = rest[:end]
	}
	h := strings.TrimSpace(rest)
	if len(h) != 40 && len(h) != 32 {
		return ""
	}
	return strings.ToUpper(h)
}

func enqueueHits(hits []searchHit, gatewayID, label string) (int, error) {
	added := 0
	magnets := make([]string, 0)
	for _, h := range hits {
		if h.Magnet != "" {
			magnets = append(magnets, h.Magnet)
		}
	}
	if len(magnets) > 0 {
		params := map[string]any{"magnets": magnets, "start": true}
		method := "fleet.torrents.add"
		if gatewayID != "" {
			method = "fleet.intake.add"
			params["gatewayId"] = gatewayID
		}
		if _, err := hostCall(method, params); err != nil {
			return added, fmt.Errorf("enqueue magnets: %w", err)
		}
		added += len(magnets)
		if label != "" {
			for _, m := range magnets {
				hash := magnetInfoHash(m)
				if hash == "" {
					continue
				}
				_, _ = hostCall("fleet.torrents.setLabel", map[string]any{"hash": hash, "label": label})
			}
		}
	}
	for _, h := range hits {
		if h.TorrentURL == "" {
			continue
		}
		b64, status, err := httpFetchBase64(h.TorrentURL)
		if err != nil || status >= 400 || b64 == "" {
			continue
		}
		params := map[string]any{"torrentBase64": b64, "start": true}
		method := "fleet.torrents.add"
		if gatewayID != "" {
			method = "fleet.intake.add"
			params["gatewayId"] = gatewayID
		}
		if _, err := hostCall(method, params); err != nil {
			return added, fmt.Errorf("enqueue torrent: %w", err)
		}
		added++
	}
	return added, nil
}

func httpFetch(rawURL string) (string, int, error) {
	raw, err := hostCall("fleet.http.fetch", map[string]any{"url": rawURL})
	if err != nil {
		return "", 0, err
	}
	var res struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", 0, err
	}
	return res.Body, res.Status, nil
}

func httpFetchBase64(rawURL string) (string, int, error) {
	raw, err := hostCall("fleet.http.fetch", map[string]any{"url": rawURL})
	if err != nil {
		return "", 0, err
	}
	var res struct {
		Status     int    `json:"status"`
		BodyBase64 string `json:"bodyBase64"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", 0, err
	}
	return res.BodyBase64, res.Status, nil
}

func loadSettings() (settings, error) {
	raw, err := hostCall("fleet.storage.get", map[string]any{"key": "settings"})
	if err != nil {
		return settings{}, err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return settings{LastFetchAt: map[string]int64{}}, nil
	}
	var st settings
	if err := json.Unmarshal(raw, &st); err != nil {
		return settings{LastFetchAt: map[string]int64{}}, nil
	}
	if st.LastFetchAt == nil {
		st.LastFetchAt = map[string]int64{}
	}
	if len(st.UserEngines) == 0 && len(st.Engines) > 0 {
		packaged := packagedNameSet()
		migrated := make([]engine, 0)
		for _, e := range st.Engines {
			if _, hit := packaged[strings.ToLower(e.Name)]; hit {
				continue
			}
			if e.MinIntervalMs <= 0 {
				e.MinIntervalMs = defaultMinIntervalMs
			}
			migrated = append(migrated, e)
		}
		st.UserEngines = migrated
		st.Engines = nil
	}
	return st, nil
}

func saveSettings(st *settings) error {
	st.Engines = nil
	_, err := hostCall("fleet.storage.put", map[string]any{"key": "settings", "value": st})
	return err
}

func hostCall(method string, params any) (json.RawMessage, error) {
	ch := make(chan hostReply, 1)
	mu.Lock()
	id := nextHost
	nextHost++
	pending[id] = ch
	mu.Unlock()

	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      id,
	})
	if err != nil {
		mu.Lock()
		delete(pending, id)
		mu.Unlock()
		return nil, err
	}
	outMu.Lock()
	fmt.Fprintf(os.Stdout, "%s\n", b)
	outMu.Unlock()

	select {
	case reply := <-ch:
		if reply.err != "" {
			return nil, fmt.Errorf("%s", reply.err)
		}
		if len(reply.result) == 0 {
			return json.RawMessage("null"), nil
		}
		return reply.result, nil
	case <-time.After(45 * time.Second):
		mu.Lock()
		delete(pending, id)
		mu.Unlock()
		return nil, fmt.Errorf("host call timeout")
	}
}

func reply(id any, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": result, "id": id})
	outMu.Lock()
	fmt.Fprintf(os.Stdout, "%s\n", b)
	outMu.Unlock()
}

func replyErr(id any, msg string) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32000, "message": msg},
		"id":      id,
	})
	outMu.Lock()
	fmt.Fprintf(os.Stdout, "%s\n", b)
	outMu.Unlock()
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
