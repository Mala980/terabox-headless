package terabox

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/mala980/terabox-headless/internal/client"
	"github.com/mala980/terabox-headless/internal/session"
)

type LoginInfo struct {
	ErrNo int `json:"errno"`
	Data  *struct {
		UserName string `json:"username"`
		UKey     string `json:"uk"`
	} `json:"data"`
}

type QuotaInfo struct {
	ErrNo int64 `json:"errno"`
	Data  *struct {
		Total    int64 `json:"total"`
		Used     int64 `json:"used"`
		Free     int64 `json:"free"`
		VIP      int   `json:"vip"`
		VIPLevel int   `json:"vip_level"`
	} `json:"data"`
}

type FileEntry struct {
	FsID        int64  `json:"fs_id"`
	Path        string `json:"path"`
	ServerName  string `json:"server_filename"`
	IsDir       int    `json:"isdir"`
	Category    int    `json:"category"`
	Size        int64  `json:"size"`
	Md5         string `json:"md5"`
	ServerMTime int64  `json:"server_mtime"`
	Thumbs      any    `json:"thumbs,omitempty"`
	Dlink       string `json:"dlink,omitempty"`
}

type ListResponse struct {
	ErrNo int         `json:"errno"`
	List  []FileEntry `json:"list"`
	Total int         `json:"total"`
}

type FileMetaResponse struct {
	ErrNo int         `json:"errno"`
	Info  []FileEntry `json:"info"`
}

func checkErrno(resp *http.Response, body []byte) (json.RawMessage, error) {
	var envelope struct {
		Errno any `json:"errno"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if n, ok := envelope.Errno.(float64); ok && n != 0 {
		return nil, fmt.Errorf("terabox API error errno=%.0f body=%s", n, string(body))
	}
	if s, ok := envelope.Errno.(string); ok && s != "" && s != "0" {
		return nil, fmt.Errorf("terabox API error errno=%s body=%s", s, string(body))
	}
	return body, nil
}

func GetLoginInfo(cl *client.Client, jsToken string) (*LoginInfo, error) {
	q := client.BuildCommonQuery(jsToken, "")
	u := client.BuildAPIURL(cl.BaseURL(), "/api/check/login", q)
	resp, err := cl.DoJSON("GET", u, nil, map[string]string{
		"Referer": cl.BaseURL() + "/main?category=all&path=%2F",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var info LoginInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse login info: %w", err)
	}
	return &info, nil
}

func GetQuota(cl *client.Client, jsToken string) (*QuotaInfo, error) {
	q := client.BuildCommonQuery(jsToken, "")
	u := client.BuildAPIURL(cl.BaseURL(), "/api/quota", q)
	resp, err := cl.DoJSON("GET", u, nil, map[string]string{
		"Referer": cl.BaseURL() + "/main?category=all&path=%2F",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var quota QuotaInfo
	if err := json.Unmarshal(body, &quota); err != nil {
		return nil, fmt.Errorf("parse quota: %w", err)
	}
	return &quota, nil
}

func ListFiles(cl *client.Client, sess *session.Session, dir string, limit int) (*ListResponse, error) {
	if dir == "" {
		dir = "/"
	}
	q := client.BuildCommonQuery(sess.GetJSToken(), sess.GetBDSToken())
	q.Set("dir", dir)
	q.Set("folder", "0")
	q.Set("num", fmt.Sprintf("%d", limit))
	q.Set("page", "1")
	u := client.BuildAPIURL(cl.BaseURL(), "/api/list", q)
	resp, err := cl.DoJSON("GET", u, nil, map[string]string{
		"Referer": cl.BaseURL() + "/main?category=all&path=%2F",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var list ListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse list: %w", err)
	}
	if list.ErrNo != 0 {
		return nil, fmt.Errorf("list error errno=%d", list.ErrNo)
	}
	return &list, nil
}

func GetFileMeta(cl *client.Client, sess *session.Session, path string) (*FileMetaResponse, error) {
	target, _ := json.Marshal([]string{path})
	q := client.BuildCommonQuery(sess.GetJSToken(), sess.GetBDSToken())
	q.Set("dlink", "1")
	q.Set("target", string(target))
	u := client.BuildAPIURL(cl.BaseURL(), "/api/filemetas", q)
	resp, err := cl.DoJSON("GET", u, nil, map[string]string{
		"Referer": cl.BaseURL() + "/main?category=all&path=%2F",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var meta FileMetaResponse
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parse filemeta: %w", err)
	}
	if meta.ErrNo != 0 {
		return nil, fmt.Errorf("filemeta error errno=%d", meta.ErrNo)
	}
	return &meta, nil
}

func GetDownloadLink(cl *client.Client, sess *session.Session, path string) (string, error) {
	meta, err := GetFileMeta(cl, sess, path)
	if err != nil {
		return "", err
	}
	if len(meta.Info) == 0 {
		return "", fmt.Errorf("no file info returned")
	}
	if meta.Info[0].Dlink == "" {
		return "", fmt.Errorf("no dlink returned for %s", path)
	}
	return meta.Info[0].Dlink, nil
}

func BuildURL(raw string, query url.Values) string {
	return client.BuildAPIURL(raw, "", query)
}
