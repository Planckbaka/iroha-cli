package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// ---------------------------------------------------------------------------
// Rate limiter (shared by web_fetch and web_search)
// ---------------------------------------------------------------------------

type rateLimiter struct {
	mu       sync.Mutex
	requests []time.Time
	max      int
	window   time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window}
}

func (rl *rateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Trim old entries
	var valid []time.Time
	for _, t := range rl.requests {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	rl.requests = valid

	if len(rl.requests) >= rl.max {
		return false
	}
	rl.requests = append(rl.requests, now)
	return true
}

var (
	webFetchRateLimiter  = newRateLimiter(10, time.Minute)
	webSearchRateLimiter = newRateLimiter(10, time.Minute)
)

// ---------------------------------------------------------------------------
// Private IP / SSRF protection
// ---------------------------------------------------------------------------

var privateNets = []net.IPNet{
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("::1/128"),
	mustParseCIDR("fe80::/10"),
	mustParseCIDR("fc00::/7"),
}

func mustParseCIDR(s string) net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return *n
}

func isPrivateIP(ip net.IP) bool {
	// Check IPv4-mapped IPv6 addresses (e.g. ::ffff:10.0.0.1)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func checkSSRF(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname in URL")
	}

	// Resolve hostname
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname %q: %w", host, err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("SSRF blocked: hostname %q resolves to private IP %s", host, ip)
		}
	}
	return nil
}

// ssrfSafeTransport validates resolved IPs at connection time to prevent DNS rebinding.
var ssrfSafeTransport = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address: %w", err)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
		}
		for _, ip := range ips {
			if isPrivateIP(ip.IP) {
				return nil, fmt.Errorf("SSRF blocked: %s resolves to private IP %s", host, ip.IP)
			}
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	},
}

var ssrfSafeClient = &http.Client{
	Transport: ssrfSafeTransport,
	Timeout:   30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := checkSSRF(req.URL); err != nil {
				return err
			}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// HTML → text conversion
// ---------------------------------------------------------------------------

var multiNewlineRe = regexp.MustCompile(`\n{3,}`)

func htmlToText(r io.Reader) string {
	doc, err := html.Parse(r)
	if err != nil {
		return ""
	}

	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				b.WriteString(text)
				b.WriteString(" ")
			}
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "iframe":
				return
			case "br":
				b.WriteString("\n")
			case "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr":
				b.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr":
				b.WriteString("\n")
			}
		}
	}
	f(doc)

	// Collapse multiple blank lines
	text := b.String()
	text = multiNewlineRe.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

const maxFetchSize = 5 * 1024 * 1024 // 5 MB
