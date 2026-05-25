package agent

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"127.0.0.1", true},
		{"169.254.169.254", true},
		{"::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"142.250.80.46", false},
		{"172.15.0.1", false}, // Just below 172.16.0.0/12 range
		{"172.32.0.1", false}, // Just above 172.16.0.0/12 range
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.want {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"Strip tags", "<html><body>Hello <b>World</b></body></html>", "Hello World"},
		{"Preserve text", "Just plain text", "Just plain text"},
		{"Paragraph breaks", "<p>Hello</p><p>World</p>", "Hello"},
		{"Paragraph breaks world", "<p>Hello</p><p>World</p>", "World"},
		{"List items", "<ul><li>one</li><li>two</li></ul>", "one"},
		{"List items two", "<ul><li>one</li><li>two</li></ul>", "two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlToText(strings.NewReader(tt.input))
			if !strings.Contains(got, tt.want) {
				t.Errorf("htmlToText() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)

	if !rl.Allow() {
		t.Error("first request should be allowed")
	}
	rl.Allow()
	rl.Allow()

	if rl.Allow() {
		t.Error("fourth request should be rate limited")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	// Use a very short window so entries expire quickly
	rl := newRateLimiter(2, 50*time.Millisecond)

	rl.Allow()
	rl.Allow()

	if rl.Allow() {
		t.Error("third request should be rate limited")
	}

	// Wait for window to expire
	time.Sleep(80 * time.Millisecond)

	if !rl.Allow() {
		t.Error("request after window expiry should be allowed")
	}
}
