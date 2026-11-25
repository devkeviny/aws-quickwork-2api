package outbound

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Proxy struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"-"`
}

type Source interface {
	ProxyURL(context.Context, string) (string, error)
}

type Router struct {
	source     Source
	defaultID  string
	mu         sync.Mutex
	transports map[string]*http.Transport
}

var directTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return transport
}()

func New(source Source, defaultID string) *Router {
	return &Router{source: source, defaultID: defaultID, transports: make(map[string]*http.Transport)}
}

func ParseList(text string) ([]Proxy, error) {
	proxies := make([]Proxy, 0)
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 4096), 16<<10)
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		parsed, err := parseProxy(value)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy on line %d: %w", line, err)
		}
		digest := sha256.Sum256([]byte(parsed.String()))
		id := hex.EncodeToString(digest[:])
		if !seen[id] {
			seen[id] = true
			proxies = append(proxies, Proxy{ID: id, Label: parsed.Scheme + "://" + parsed.Host, URL: parsed.String()})
		}
		if len(proxies) > 1000 {
			return nil, errors.New("at most 1000 proxies can be imported at once")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("a proxy line exceeds 16 KB")
	}
	if len(proxies) == 0 {
		return nil, errors.New("enter at least one proxy URL")
	}
	return proxies, nil
}

func parseProxy(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("expected an http:// or socks5:// URL without a path, query or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "socks5" {
		return nil, errors.New("supported proxy schemes are http and socks5")
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "http" {
			port = "80"
		} else {
			port = "1080"
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, errors.New("proxy port must be between 1 and 65535")
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		if parsed.Scheme == "socks5" && (len(parsed.User.Username()) > 255 || len(password) > 255) {
			return nil, errors.New("SOCKS5 credentials must not exceed 255 bytes each")
		}
		parsed.User = url.UserPassword(parsed.User.Username(), password)
	}
	parsed.Host = net.JoinHostPort(strings.ToLower(parsed.Hostname()), strconv.Itoa(number))
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed, nil
}

func (router *Router) Has(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := router.resolve(ctx, id)
	return err == nil
}

func (router *Router) resolve(ctx context.Context, id string) (*url.URL, error) {
	if id == "" && router != nil {
		id = router.defaultID
	}
	if id == "" || id == "direct" {
		return nil, nil
	}
	if router == nil || router.source == nil {
		return nil, errors.New("bound proxy is unavailable")
	}
	value, err := router.source.ProxyURL(ctx, id)
	if err != nil {
		return nil, errors.New("bound proxy is unavailable")
	}
	return parseProxy(value)
}

func (router *Router) Client(ctx context.Context, id string, timeout time.Duration) (*http.Client, error) {
	parsed, err := router.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	if id == "direct" || (id == "" && router != nil && router.defaultID == "direct") {
		client.Transport = directTransport
	} else if parsed != nil {
		router.mu.Lock()
		transport := router.transports[parsed.String()]
		if transport == nil {
			transport = http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = http.ProxyURL(parsed)
			router.transports[parsed.String()] = transport
		}
		router.mu.Unlock()
		client.Transport = transport
	}
	return client, nil
}

func (router *Router) Dialer(ctx context.Context, id string) (websocket.Dialer, error) {
	dialer := *websocket.DefaultDialer
	parsed, err := router.resolve(ctx, id)
	if err != nil {
		return dialer, err
	}
	if id == "direct" || (id == "" && router != nil && router.defaultID == "direct") {
		dialer.Proxy = nil
	} else if parsed != nil {
		dialer.Proxy = http.ProxyURL(parsed)
	}
	return dialer, nil
}

func (router *Router) CloseIdleConnections() {
	if router == nil {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	for _, transport := range router.transports {
		transport.CloseIdleConnections()
	}
	clear(router.transports)
}
