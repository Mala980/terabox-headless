package session

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type Session struct {
	mu       sync.RWMutex
	NDUS     string            `json:"ndus"`
	BaseURL  string            `json:"baseUrl"`
	Cookies  map[string]string `json:"cookies"`
	JSToken  string            `json:"jsToken"`
	BDSToken string            `json:"bdstoken"`
	filePath string
}

func configDir() (string, error) {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		xdg = filepath.Join(home, ".config")
	}
	dir := filepath.Join(xdg, "terabox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func Load() (*Session, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "session.json")
	s := &Session{
		Cookies:  make(map[string]string),
		filePath: path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("session file corrupt: %w", err)
	}
	if s.Cookies == nil {
		s.Cookies = make(map[string]string)
	}
	return s, nil
}

func (s *Session) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.filePath == "" {
		dir, err := configDir()
		if err != nil {
			return err
		}
		s.filePath = filepath.Join(dir, "session.json")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}

func (s *Session) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NDUS = ""
	s.BaseURL = ""
	s.Cookies = make(map[string]string)
	s.JSToken = ""
	s.BDSToken = ""
	if s.filePath != "" {
		os.Remove(s.filePath)
	}
	return nil
}

func (s *Session) LoggedIn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.NDUS != ""
}

func (s *Session) GetNDUS() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.NDUS
}

func (s *Session) GetBaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.BaseURL == "" {
		return "https://dm.nephobox.com"
	}
	return s.BaseURL
}

func (s *Session) GetJSToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.JSToken
}

func (s *Session) GetBDSToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.BDSToken
}

func (s *Session) GetCookies() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]string, len(s.Cookies))
	for k, v := range s.Cookies {
		cp[k] = v
	}
	return cp
}

func (s *Session) SetLoginData(ndus, baseURL string, cookies map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NDUS = ndus
	s.BaseURL = baseURL
	for k, v := range cookies {
		s.Cookies[k] = v
	}
}

func (s *Session) SetTokens(jsToken, bdstoken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.JSToken = jsToken
	s.BDSToken = bdstoken
}

func (s *Session) MergeCookies(cookies map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range cookies {
		s.Cookies[k] = v
	}
}

func (s *Session) NeedsRefresh() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.JSToken == "" || s.BDSToken == ""
}

var (
	jsTokenRE   = regexp.MustCompile(`(?:fn%28%22|fn\("|"jsToken":")([A-Fa-f0-9]{32,512})(?:%22\)|"\)|")`)
	bdstokenRE  = regexp.MustCompile(`"bdstoken":"([^"]+)"`)
	pcftokenRE  = regexp.MustCompile(`"pcftoken":"([^"]+)"`)
)

func ExtractJSToken(html string) string {
	matches := jsTokenRE.FindStringSubmatch(html)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func ExtractBDSToken(html string) string {
	matches := bdstokenRE.FindStringSubmatch(html)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func ExtractPCFToken(html string) string {
	matches := pcftokenRE.FindStringSubmatch(html)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func ParseCookiesFromHeader(header string) map[string]string {
	cookies := make(map[string]string)
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.IndexByte(part, '=')
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(part[:idx])
		val := strings.TrimSpace(part[idx+1:])
		if name != "" {
			cookies[name] = val
		}
	}
	return cookies
}

func MergeSetCookies(existing map[string]string, setCookieHeaders []string) {
	for _, sc := range setCookieHeaders {
		parts := strings.SplitN(sc, ";", 2)
		if len(parts) == 0 {
			continue
		}
		kv := strings.TrimSpace(parts[0])
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(kv[:idx])
		val := strings.TrimSpace(kv[idx+1:])
		if name != "" {
			existing[name] = val
		}
	}
}

type RefreshResult struct {
	JSToken  string
	BDSToken string
	Cookies  map[string]string
	BaseURL  string
}

func (s *Session) RefreshFromMainPage(body string, setCookies []string, finalURL string) *RefreshResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	jsToken := ExtractJSToken(body)
	bdstoken := ExtractBDSToken(body)

	if jsToken != "" {
		s.JSToken = jsToken
	}
	if bdstoken != "" {
		s.BDSToken = bdstoken
	}

	MergeSetCookies(s.Cookies, setCookies)

	if finalURL != "" {
		parsed, err := url.Parse(finalURL)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			s.BaseURL = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		}
	}

	return &RefreshResult{
		JSToken:  s.JSToken,
		BDSToken: s.BDSToken,
		Cookies:  copyMap(s.Cookies),
		BaseURL:  s.BaseURL,
	}
}

func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func (s *Session) SetCookieJar(cookies map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cookies = cookies
}