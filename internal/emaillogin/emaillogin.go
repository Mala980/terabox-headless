package emaillogin

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mala980/terabox-headless/internal/client"
	"github.com/mala980/terabox-headless/internal/session"
)

type publicKeyResponse struct {
	Errno  string `json:"errno"`
	PubKey string `json:"pubkey"`
	Key    string `json:"key"`
}

var (
	getapiTokenRE = regexp.MustCompile(`"token"\s*:\s*"([^"]+)"`)
	getapiCodeRE  = regexp.MustCompile(`"codeString"\s*:\s*"([^"]*)"`)
	pubkeyRE      = regexp.MustCompile(`"pubkey"\s*:\s*'([^']*)'`)
	rsaKeyRE      = regexp.MustCompile(`"key"\s*:\s*'([^']*)'`)
)

type loginResponse struct {
	ErrInfo *struct {
		No  string `json:"no"`
		Msg string `json:"msg"`
	} `json:"errInfo"`
	Data *struct {
		BDUSS string `json:"bduss"`
		BDS   string `json:"bds"`
		U     string `json:"u"`
	} `json:"data"`
	Code int `json:"code"`
}

func doLogin(cl *client.Client, email, password string) (string, error) {
	ts := fmt.Sprintf("%d000", time.Now().UnixMilli())

	_, err := cl.Do("GET", "https://passport.baidu.com/v2/login", nil, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return "", fmt.Errorf("fetch login page: %w", err)
	}

	getapiURL := "https://passport.baidu.com/v2/api/?getapi&tpl=pp&apiver=v3&tt=" + ts + "&class=login"
	resp, err := cl.DoJSON("GET", getapiURL, nil, map[string]string{
		"Referer": "https://passport.baidu.com/v2/login",
	})
	if err != nil {
		return "", fmt.Errorf("getapi: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	getapiBody := string(body)
	tokenMatch := getapiTokenRE.FindStringSubmatch(getapiBody)
	if len(tokenMatch) < 2 || tokenMatch[1] == "" {
		return "", fmt.Errorf("getapi: no token in response: %s", truncate(getapiBody, 200))
	}
	token := tokenMatch[1]
	codeString := ""
	if codeMatch := getapiCodeRE.FindStringSubmatch(getapiBody); len(codeMatch) > 1 {
		codeString = codeMatch[1]
	}

	pubKeyURL := "https://passport.baidu.com/v2/getpublickey?loginVersion=v4&gid=&tt=" + ts
	resp, err = cl.DoJSON("GET", pubKeyURL, nil, map[string]string{
		"Referer":            "https://passport.baidu.com/v2/login",
		"X-Requested-With":   "XMLHttpRequest",
	})
	if err != nil {
		return "", fmt.Errorf("getpublickey: %w", err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	pubKeyBody := string(body)
	pubKeyMatch := pubkeyRE.FindStringSubmatch(pubKeyBody)
	if len(pubKeyMatch) < 2 || pubKeyMatch[1] == "" {
		return "", fmt.Errorf("getpublickey: no pubkey in response: %s", truncate(pubKeyBody, 200))
	}
	pubKey := strings.ReplaceAll(pubKeyMatch[1], `\n`, "\n")
	pubKey = strings.ReplaceAll(pubKey, `\/`, "/")
	pubKey = strings.ReplaceAll(pubKey, `\"`, `"`)

	encrypted, err := rsaEncrypt(pubKey, password)
	if err != nil {
		return "", fmt.Errorf("rsa encrypt password: %w", err)
	}

	form := url.Values{}
	form.Set("username", email)
	form.Set("password", encrypted)
	form.Set("token", token)
	form.Set("codeString", codeString)
	form.Set("staticpage", "https://passport.baidu.com/static/passpc-account/html/v3Jump.html")
	form.Set("charset", "utf-8")
	form.Set("loginType", "1")
	form.Set("tpl", "pp")
	form.Set("apiver", "v3")
	form.Set("tt", ts)
	form.Set("isPhone", "false")
	form.Set("mem_pass", "on")
	form.Set("crypttype", "12")
	form.Set("countrycode", "")
	form.Set("safeflg", "0")
	form.Set("quick_user", "0")
	form.Set("logLoginType", "pc_loginLog")
	form.Set("ppui_logintime", fmt.Sprintf("%d", time.Now().UnixMilli()%100000))
	form.Set("loginVersion", "v4")
	form.Set("FP_UID", "")
	form.Set("FP_INFO", "")
	form.Set("client", "")

	loginURL := "https://passport.baidu.com/v2/api/?login&tpl=pp&apiver=v3&tt=" + ts + "&class=login"
	resp, err = cl.DoForm("POST", loginURL, form, map[string]string{
		"Accept":           "application/json, text/plain, */*",
		"Referer":          "https://passport.baidu.com/v2/login",
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}

	// Baidu login may return redirect HTML or JSON. Check cookies first.
	bduss := cl.Cookies()["BDUSS"]
	if bduss == "" {
		// Try to parse JSON from response
		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		var lr loginResponse
		if err := json.Unmarshal(body, &lr); err == nil {
			if lr.Data != nil && lr.Data.BDUSS != "" {
				bduss = lr.Data.BDUSS
			}
			if bduss == "" && lr.ErrInfo != nil && lr.ErrInfo.No != "0" {
				errno := lr.ErrInfo.No
				msg := lr.ErrInfo.Msg
				if msg == "" {
					msg = "unknown error"
				}
				if errno == "257" || errno == "400031" || strings.Contains(msg, "captcha") || strings.Contains(msg, "verify") {
					return "", fmt.Errorf("captcha required (errno=%s): %s. Use QR login instead", errno, msg)
				}
				return "", fmt.Errorf("login failed (errno=%s): %s", errno, msg)
			}
		}
		// If still no BDUSS, check if login succeeded by looking at the response
		bodyStr := string(body)
		if bduss == "" && strings.Contains(bodyStr, "bduss") {
			bdussMatch := regexp.MustCompile(`"bduss"\s*:\s*"([^"]+)"`).FindStringSubmatch(bodyStr)
			if len(bdussMatch) > 1 {
				bduss = bdussMatch[1]
			}
		}
		if bduss == "" {
			// The response is a redirect page with err_no for failures
			if errNoMatch := regexp.MustCompile(`err_no=(\d+)`).FindStringSubmatch(bodyStr); len(errNoMatch) > 1 && errNoMatch[1] != "0" {
				return "", fmt.Errorf("login failed (err_no=%s)", errNoMatch[1])
			}
		}
	} else {
		resp.Body.Close()
	}

	if bduss == "" {
		return "", fmt.Errorf("login failed: no BDUSS in response. Body: %s", truncate(string(body), 600))
	}

	return bduss, nil
}

func rsaEncrypt(pubKeyPEM, password string) (string, error) {
	block, _ := pem.Decode([]byte(pubKeyPEM))
	var decoded []byte
	if block == nil {
		cleaned := strings.ReplaceAll(pubKeyPEM, "-----BEGIN PUBLIC KEY-----", "")
		cleaned = strings.ReplaceAll(cleaned, "-----END PUBLIC KEY-----", "")
		cleaned = strings.ReplaceAll(cleaned, "\n", "")
		cleaned = strings.ReplaceAll(cleaned, "\\n", "")
		var err error
		decoded, err = base64.StdEncoding.DecodeString(cleaned)
		if err != nil {
			return "", fmt.Errorf("decode pubkey: %w", err)
		}
	} else {
		decoded = block.Bytes
	}
	key, err := x509.ParsePKIXPublicKey(decoded)
	if err != nil {
		return "", fmt.Errorf("parse PKIX key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("not RSA public key")
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, rsaKey, []byte(password))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func EmailLogin(cl *client.Client, sess *session.Session, email, password string) error {
	bduss, err := doLogin(cl, email, password)
	if err != nil {
		return fmt.Errorf("email login: %w", err)
	}

	cookies := cl.Cookies()
	cookies["BDUSS"] = bduss
	cl.SetCookies(cookies)

	ndus := cookies["ndus"]
	if ndus == "" {
		ndus = bduss
	}

	baseURL := "https://dm.nephobox.com"

	resp, err := cl.Do("GET", baseURL+"/main?category=all&path=%2F", nil, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return fmt.Errorf("fetch main page: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	jsToken := session.ExtractJSToken(string(body))
	bdstoken := session.ExtractBDSToken(string(body))
	setCookies := resp.Header.Values("Set-Cookie")
	session.MergeSetCookies(cookies, setCookies)
	cl.SetCookies(cookies)

	sess.SetLoginData(ndus, baseURL, cookies)
	sess.SetTokens(jsToken, bdstoken)

	if jsToken == "" {
		return fmt.Errorf("could not extract jsToken from main page")
	}

	return sess.Save()
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}