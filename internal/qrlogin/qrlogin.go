package qrlogin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/mala980/terabox-headless/internal/client"
	"github.com/mala980/terabox-headless/internal/session"
	"github.com/mala980/terabox-headless/internal/util"
)

const (
	loginPageURL = "https://www.1024terabox.com/ai/index"
	passVersion  = "2.8"
	pollInterval = 2 * time.Second
	pollTimeout  = 120 * time.Second
)

type QRStartResult struct {
	QRDataURL  string
	UUID       string
	Seq        int
	PCFToken   string
	BrowserID  string
	LoginOrigin string
	Cookies    map[string]string
	PngData    []byte
}

type QRCheckResult struct {
	Status    string
	NDUS      string
	BaseURL   string
	JSToken   string
	BDSToken  string
	Cookies   map[string]string
	AvatarURL string
	UserName  string
}

type qrCodeResponse struct {
	Errno int `json:"errno"`
	Data  *struct {
		QRCode string `json:"qrcode"`
		UUID   string `json:"uuid"`
		Seq    int    `json:"seq"`
	} `json:"data"`
}

type qrLoginResponse struct {
	Errno int    `json:"errno"`
	Code  int    `json:"code"`
	Msg   string `json:"msg"`
	V     string `json:"v"`
	VCode string `json:"vcode"`
	Data  *struct {
		NDUS    string `json:"ndus"`
		BDUSS   string `json:"bduss"`
		UserID  string `json:"userid"`
		UName   string `json:"uname"`
		Avatar  string `json:"avatar_url"`
		V       string `json:"v"`
		VCode   string `json:"vcode"`
	} `json:"data"`
}

func Start(cl *client.Client) (*QRStartResult, error) {
	resp, err := cl.Do("GET", loginPageURL, nil, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return nil, fmt.Errorf("fetch login page: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read login page: %w", err)
	}

	pcftoken := session.ExtractPCFToken(string(body))
	if pcftoken == "" {
		return nil, fmt.Errorf("could not extract pcftoken from login page")
	}

	cookies := cl.Cookies()
	browserID := cookies["browserid"]
	if browserID == "" {
		return nil, fmt.Errorf("browserid cookie not set")
	}

	loginOrigin := "https://www.1024terabox.com"

	form := url.Values{}
	form.Set("browserid", browserID)
	form.Set("client", "web")
	form.Set("clientfrom", "h5")
	form.Set("lang", "en")
	form.Set("pass_version", passVersion)
	form.Set("pcftoken", pcftoken)

	qrResp, err := cl.DoForm("POST", loginOrigin+"/passport/qrcode/get", form, map[string]string{
		"Accept":  "application/json, text/plain, */*",
		"Origin":  "https://www.1024terabox.com",
		"Referer": loginPageURL,
	})
	if err != nil {
		return nil, fmt.Errorf("QR code request: %w", err)
	}
	qrBody, err := io.ReadAll(qrResp.Body)
	qrResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read QR response: %w", err)
	}

	var qrRes qrCodeResponse
	if err := json.Unmarshal(qrBody, &qrRes); err != nil {
		return nil, fmt.Errorf("parse QR response: %w", err)
	}
	if qrRes.Errno != 0 || qrRes.Data == nil {
		return nil, fmt.Errorf("QR code API error: errno=%d", qrRes.Errno)
	}

	pngData, err := util.DecodeQRDataURL(qrRes.Data.QRCode)
	if err != nil {
		return nil, fmt.Errorf("decode QR image: %w", err)
	}

	return &QRStartResult{
		QRDataURL:   qrRes.Data.QRCode,
		UUID:        qrRes.Data.UUID,
		Seq:         qrRes.Data.Seq,
		PCFToken:    pcftoken,
		BrowserID:   browserID,
		LoginOrigin: loginOrigin,
		Cookies:     cl.Cookies(),
		PngData:     pngData,
	}, nil
}

func Poll(cl *client.Client, result *QRStartResult) (*QRCheckResult, error) {
	startTime := time.Now()

	for {
		if time.Since(startTime) > pollTimeout {
			return nil, fmt.Errorf("QR login timed out after %v", pollTimeout)
		}

		form := url.Values{}
		form.Set("browserid", result.BrowserID)
		form.Set("client", "web")
		form.Set("clientfrom", "h5")
		form.Set("lang", "en")
		form.Set("pass_version", passVersion)
		form.Set("pcftoken", result.PCFToken)
		form.Set("reg_source", "web")
		form.Set("seq", fmt.Sprintf("%d", result.Seq))
		form.Set("step", "0")
		form.Set("uuid", result.UUID)

		resp, err := cl.DoForm("POST", result.LoginOrigin+"/passport/qrcode/login", form, map[string]string{
			"Accept":  "application/json, text/plain, */*",
			"Origin":  "https://www.1024terabox.com",
			"Referer": loginPageURL,
		})
		if err != nil {
			return nil, fmt.Errorf("QR poll request: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read QR poll: %w", err)
		}

		var loginRes qrLoginResponse
		if err := json.Unmarshal(body, &loginRes); err != nil {
			return nil, fmt.Errorf("parse QR poll: %w", err)
		}

		code := loginRes.Errno
		if code == 0 {
			code = loginRes.Code
		}

		if code == 39 {
			time.Sleep(pollInterval)
			continue
		}

		if code == 0 {
			// Success! Get ndus from response or cookies
			ndus := ""
			if loginRes.Data != nil && loginRes.Data.NDUS != "" {
				ndus = loginRes.Data.NDUS
			}
			if ndus == "" {
				cookies := cl.Cookies()
				ndus = cookies["ndus"]
			}
			if ndus == "" && loginRes.Data != nil && loginRes.Data.BDUSS != "" {
				ndus = loginRes.Data.BDUSS
			}
			if ndus == "" {
				return nil, fmt.Errorf("QR login succeeded but ndus not found")
			}

			userName := ""
			avatarURL := ""
			if loginRes.Data != nil {
				userName = loginRes.Data.UName
				avatarURL = loginRes.Data.Avatar
			}

			return &QRCheckResult{
				Status:    "success",
				NDUS:      ndus,
				Cookies:   cl.Cookies(),
				AvatarURL: avatarURL,
				UserName:  userName,
			}, nil
		}

		return nil, fmt.Errorf("QR login failed: code=%d msg=%s", code, loginRes.Msg)
	}
}

func CompleteLoginWithNDUS(cl *client.Client, sess *session.Session, ndus string) error {
	baseURL := "https://dm.nephobox.com"
	sess.SetCookieJar(cl.Cookies())

	if !strings.Contains(cl.CookieHeader(), "ndus=") {
		cookies := cl.Cookies()
		cookies["ndus"] = ndus
		cl.SetCookies(cookies)
	}

	resp, err := cl.Do("GET", baseURL+"/main?category=all&path=%2F", nil, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return fmt.Errorf("fetch main page: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("read main page: %w", err)
	}

	jsToken := session.ExtractJSToken(string(body))
	bdstoken := session.ExtractBDSToken(string(body))
	setCookies := resp.Header.Values("Set-Cookie")

	baseURL = "https://dm.nephobox.com"

	cookies := cl.Cookies()
	session.MergeSetCookies(cookies, setCookies)
	cl.SetCookies(cookies)

	sess.SetLoginData(ndus, baseURL, cookies)
	sess.SetTokens(jsToken, bdstoken)

	if jsToken == "" {
		return fmt.Errorf("could not extract jsToken from main page")
	}

	return sess.Save()
}