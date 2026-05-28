package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"google.golang.org/adk/tool"

	"iroha/pkg/config"
)


type WebFetchArgs struct {
	URL     string `json:"url" description:"The HTTP(S) URL to fetch"`
	Timeout int    `json:"timeout,omitempty" description:"Request timeout in seconds (default 20)"`
}

type WebFetchResult struct {
	Content  string `json:"content" description:"The fetched content as markdown"`
	MimeType string `json:"mime_type" description:"The detected MIME type"`
	Length   int    `json:"length" description:"Content length in bytes"`
}

func WebFetchHandler(ctx tool.Context, args WebFetchArgs) (WebFetchResult, error) {
	// Rate limit
	if !webFetchRateLimiter.Allow() {
		return WebFetchResult{}, fmt.Errorf("web_fetch rate limit exceeded: max 10 requests per minute")
	}

	// Validate URL
	parsed, err := url.Parse(args.URL)
	if err != nil {
		return WebFetchResult{}, WrapToolError("web_fetch", args, fmt.Errorf("invalid URL: %w", err))
	}
	if scheme := parsed.Scheme; scheme != "http" && scheme != "https" {
		return WebFetchResult{}, fmt.Errorf("web_fetch: only http and https schemes are allowed, got %q", scheme)
	}

	// SSRF check
	if err := checkSSRF(parsed); err != nil {
		return WebFetchResult{}, WrapToolError("web_fetch", args, err)
	}

	// Timeout
	timeout := 20
	if args.Timeout > 0 {
		timeout = args.Timeout
	}
	if timeout > 60 {
		timeout = 60
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, args.URL, nil)
	if err != nil {
		return WebFetchResult{}, WrapToolError("web_fetch", args, err)
	}
	req.Header.Set("User-Agent", "iroha-code/1.0")

	resp, err := ssrfSafeClient.Do(req)
	if err != nil {
		return WebFetchResult{}, WrapToolError("web_fetch", args, fmt.Errorf("HTTP request failed: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebFetchResult{}, fmt.Errorf("web_fetch: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Read body with size limit
	lr := io.LimitReader(resp.Body, maxFetchSize+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return WebFetchResult{}, WrapToolError("web_fetch", args, fmt.Errorf("failed to read response body: %w", err))
	}
	truncated := false
	if len(body) > maxFetchSize {
		body = body[:maxFetchSize]
		truncated = true
	}

	ct := resp.Header.Get("Content-Type")
	mimeType := ct
	if idx := strings.Index(ct, ";"); idx != -1 {
		mimeType = strings.TrimSpace(ct[:idx])
	}

	// Convert HTML to text
	var content string
	if strings.HasPrefix(mimeType, "text/html") {
		content = htmlToText(strings.NewReader(string(body)))
	} else {
		content = string(body)
	}

	if truncated {
		content += "\n\n[Warning: Content truncated due to 5MB size limit]"
	}

	return WebFetchResult{
		Content:  content,
		MimeType: mimeType,
		Length:   len(body),
	}, nil
}

// ---------------------------------------------------------------------------
// web_search
// ---------------------------------------------------------------------------

type WebSearchArgs struct {
	Query string `json:"query" description:"The search query string"`
	Count int    `json:"count,omitempty" description:"Number of results (default 5, max 10)"`
}

type WebSearchResult struct {
	Results []SearchResult `json:"results" description:"Search results"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func WebSearchHandler(ctx tool.Context, args WebSearchArgs) (WebSearchResult, error) {
	// Rate limit
	if !webSearchRateLimiter.Allow() {
		return WebSearchResult{}, fmt.Errorf("web_search rate limit exceeded: max 10 searches per minute")
	}

	count := 5
	if args.Count > 0 {
		count = args.Count
	}
	if count > 10 {
		count = 10
	}

	// Check for SearXNG backend
	if cfg, err := config.LoadConfig(); err == nil {
		if su := cfg.WebSearchSearXNGURL; su != "" {
			return searxngSearch(su, args.Query, count)
		}
	}

	// Default: DuckDuckGo HTML scraping
	return duckduckgoSearch(args.Query, count)
}

// duckduckgoSearch scrapes DuckDuckGo HTML search results.
func duckduckgoSearch(query string, count int) (WebSearchResult, error) {
	u := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, err)
	}
	req.Header.Set("User-Agent", "iroha-code/1.0")

	resp, err := ssrfSafeClient.Do(req)
	if err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, fmt.Errorf("DuckDuckGo request failed: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return WebSearchResult{}, fmt.Errorf("web_search: DuckDuckGo returned HTTP %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, fmt.Errorf("failed to parse DuckDuckGo HTML: %w", err))
	}

	var results []SearchResult
	parseDDGResults(doc, &results, count)

	return WebSearchResult{Results: results}, nil
}

// parseDDGResults walks the DuckDuckGo HTML page and extracts result entries.
func parseDDGResults(n *html.Node, results *[]SearchResult, max int) {
	if len(*results) >= max {
		return
	}

	// DuckDuckGo HTML results are in <div class="result results_links results_links_deep web-result">
	if n.Type == html.ElementNode && n.Data == "div" {
		cls := getAttr(n, "class")
		if strings.Contains(cls, "result") && strings.Contains(cls, "web-result") {
			sr := SearchResult{}
			extractDDGResult(n, &sr)
			if sr.Title != "" && sr.URL != "" {
				*results = append(*results, sr)
			}
			return
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		parseDDGResults(c, results, max)
	}
}

func extractDDGResult(n *html.Node, sr *SearchResult) {
	if n.Type == html.ElementNode {
		cls := getAttr(n, "class")

		// Title + URL: <a class="result__a" href="...">
		if n.Data == "a" && strings.Contains(cls, "result__a") {
			sr.Title = textContent(n)
			href := getAttr(n, "href")
			// DuckDuckGo wraps URLs through //duckduckgo.com/l/?uddg=<encoded>&rut=...
			if strings.Contains(href, "uddg=") {
				if u, err := url.Parse(href); err == nil {
					if enc := u.Query().Get("uddg"); enc != "" {
						sr.URL = enc
					}
				}
			}
			if sr.URL == "" {
				sr.URL = href
			}
			return
		}

		// Snippet: <a class="result__snippet">
		if n.Data == "a" && strings.Contains(cls, "result__snippet") {
			sr.Snippet = textContent(n)
			return
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractDDGResult(c, sr)
	}
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c))
	}
	return strings.TrimSpace(b.String())
}

// searxngSearch queries a SearXNG instance for search results.
func searxngSearch(searxngURL, query string, count int) (WebSearchResult, error) {
	u := fmt.Sprintf("%s/search?q=%s&format=json", strings.TrimRight(searxngURL, "/"), url.QueryEscape(query))

	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, err)
	}
	req.Header.Set("User-Agent", "iroha-code/1.0")

	resp, err := ssrfSafeClient.Do(req)
	if err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, fmt.Errorf("SearXNG request failed: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return WebSearchResult{}, fmt.Errorf("web_search: SearXNG returned HTTP %d", resp.StatusCode)
	}

	// SearXNG JSON: {"results":[{"title":"...","url":"...","content":"..."},...]}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchSize))
	if err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, err)
	}

	// Minimal JSON parsing without importing encoding/json for the whole struct
	type searxngItem struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}
	type searxngResp struct {
		Results []searxngItem `json:"results"`
	}

	// Use encoding/json for simplicity
	var sr searxngResp
	if err := json.Unmarshal(body, &sr); err != nil {
		return WebSearchResult{}, WrapToolError("web_search", WebSearchArgs{Query: query}, fmt.Errorf("failed to parse SearXNG response: %w", err))
	}

	results := make([]SearchResult, 0, count)
	for i, r := range sr.Results {
		if i >= count {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}

	return WebSearchResult{Results: results}, nil
}


func registerWebTools(r *ToolRegistry) {
	register(r, "web_fetch", "Fetch a web page by URL and return its content as text. Only HTTP/HTTPS is supported. Private IP ranges are blocked for security. Response body limited to 1MB.", WebFetchHandler)
	register(r, "web_search", "Search the web using DuckDuckGo (default) or a configured SearXNG backend. Returns structured results with title, URL, and snippet.", WebSearchHandler)
}
