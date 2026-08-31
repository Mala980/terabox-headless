package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	AppID      = "250528"
	Channel    = "dubox"
	ClientType = "0"
	UserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
)

type Client struct {
	httpClient *http.Client
	cookies    map[string]string
	baseURL    string
}

func New(baseURL string) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
		DialContext:     customDialContext,
	}
	return &Client{
		httpClient: &http.Client{
			Transport: tr,
			Timeout:   60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				if c := req.Response.Cookies(); len(c) > 0 {
					for _, ck := range c {
						if ck.Name != "" {
							ck.Value = strings.TrimSpace(ck.Value)
						}
					}
				}
				return nil
			},
		},
		cookies: make(map[string]string),
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

var dnsServers = loadDNSServers()

func loadDNSServers() []string {
	var servers []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			servers = append(servers, s)
		}
	}
	paths := []string{
		"/data/data/com.termux/files/usr/etc/resolv.conf",
		"/etc/resolv.conf",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "nameserver" {
				add(fields[1])
			}
		}
	}
	add("8.8.8.8")
	add("1.1.1.1")
	return servers
}

var resolver = &net.Resolver{
	PreferGo: true,
	Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: 5 * time.Second}
		var lastErr error
		for _, server := range dnsServers {
			addr := net.JoinHostPort(server, "53")
			conn, err := d.DialContext(ctx, network, addr)
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no DNS servers available")
		}
		return nil, lastErr
	},
}

func customDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, address)
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	var lastErr error
	for _, ip := range ips {
		addr := net.JoinHostPort(ip.String(), port)
		conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) SetCookies(cookies map[string]string) {
	for k, v := range cookies {
		c.cookies[k] = v
	}
}

func (c *Client) Cookies() map[string]string {
	cp := make(map[string]string, len(c.cookies))
	for k, v := range c.cookies {
		cp[k] = v
	}
	return cp
}

func (c *Client) CookieHeader() string {
	var parts []string
	for k, v := range c.cookies {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "; ")
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) SetBaseURL(u string) {
	c.baseURL = strings.TrimRight(u, "/")
}

func (c *Client) mergeSetCookies(setCookies []string) {
	for _, sc := range setCookies {
		parts := strings.SplitN(sc, ";", 2)
		if len(parts) == 0 {
			continue
		}
		kv := strings.TrimSpace(parts[0])
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			name := strings.TrimSpace(kv[:idx])
			val := strings.TrimSpace(kv[idx+1:])
			if name != "" {
				c.cookies[name] = val
			}
		}
	}
}

func (c *Client) Do(method, reqURL string, body io.Reader, extraHeaders map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if extraHeaders != nil {
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("Cookie") == "" {
		req.Header.Set("Cookie", c.CookieHeader())
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
		c.mergeSetCookies(sc)
	}
	return resp, nil
}

func (c *Client) DoForm(method, reqURL string, formData url.Values, extraHeaders map[string]string) (*http.Response, error) {
	body := strings.NewReader(formData.Encode())
	h := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
	}
	for k, v := range extraHeaders {
		h[k] = v
	}
	return c.Do(method, reqURL, body, h)
}

func (c *Client) Raw(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	if req.Header.Get("Cookie") == "" {
		req.Header.Set("Cookie", c.CookieHeader())
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
		c.mergeSetCookies(sc)
	}
	return resp, nil
}

func (c *Client) DoJSON(method, reqURL string, body io.Reader, extraHeaders map[string]string) (*http.Response, error) {
	h := map[string]string{
		"Accept": "application/json, text/plain, */*",
	}
	for k, v := range extraHeaders {
		h[k] = v
	}
	return c.Do(method, reqURL, body, h)
}

func BuildAPIURL(baseURL, endpoint string, query url.Values) string {
	u := strings.TrimRight(baseURL, "/") + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func BuildCommonQuery(jsToken, bdstoken string) url.Values {
	q := url.Values{}
	q.Set("app_id", AppID)
	q.Set("web", "1")
	q.Set("channel", Channel)
	q.Set("clienttype", ClientType)
	if jsToken != "" {
		q.Set("jsToken", jsToken)
	}
	q.Set("dp-logid", fmt.Sprintf("%d", time.Now().UnixNano()/int64(time.Millisecond)))
	if bdstoken != "" {
		q.Set("bdstoken", bdstoken)
	}
	return q
}