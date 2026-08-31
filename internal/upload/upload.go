package upload

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mala980/terabox-headless/internal/client"
	"github.com/mala980/terabox-headless/internal/session"
)

const (
	chunkSize     = 4 * 1024 * 1024
	sliceMD5Size  = 256 * 1024
	appID         = "250528"
)

type precreateResponse struct {
	Errno      int      `json:"errno"`
	UploadID   string   `json:"uploadid"`
	ReturnType int      `json:"return_type"`
	Hosts      []string `json:"host"`
	Path       string   `json:"path"`
}

type chunkUploadResponse struct {
	Errno    int    `json:"errno"`
	ErrorNo  int    `json:"error_code"`
	ErrorMsg string `json:"error_msg"`
	MD5      string `json:"md5"`
}

type createResponse struct {
	Errno int    `json:"errno"`
	Path  string `json:"path"`
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func UploadFile(cl *client.Client, sess *session.Session, localPath, remotePath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fileInfo, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fileInfo.Size()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	if remotePath == "" {
		remotePath = "/" + filepath.Base(localPath)
	}
	remotePath = normalizePath(remotePath)

	contentMD5 := md5Hex(data)
	sliceMD5 := md5Hex(data[:min(int(size), sliceMD5Size)])
	localMTime := time.Now().Unix()

	var chunks []struct {
		MD5 string
	}
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, struct{ MD5 string }{MD5: md5Hex(data[offset:end])})
	}
	if len(chunks) == 0 {
		chunks = append(chunks, struct{ MD5 string }{MD5: md5Hex(nil)})
	}
	blockList, _ := json.Marshal(chunkMD5s(chunks))

	precreate, err := precreate(cl, sess, remotePath, size, contentMD5, sliceMD5, localMTime, blockList)
	if err != nil {
		return "", err
	}

	if precreate.ReturnType == 2 {
		return precreate.Path, nil
	}

	hosts, err := locateUploadHosts(cl, sess, remotePath, precreate.UploadID)
	if err != nil {
		hosts = buildFallbackHosts(cl.BaseURL())
	}

	uploadedMD5s, err := uploadChunks(cl, sess, hosts, remotePath, precreate.UploadID, data, chunks)
	if err != nil {
		return "", err
	}
	finalBlockList, _ := json.Marshal(uploadedMD5s)

	return create(cl, sess, remotePath, size, contentMD5, sliceMD5, localMTime, finalBlockList, precreate.UploadID)
}

func chunkMD5s(chunks []struct{ MD5 string }) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.MD5
	}
	return out
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/upload.bin"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func parentPath(p string) string {
	p = normalizePath(p)
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) <= 1 {
		return "/"
	}
	return "/" + strings.Join(parts[:len(parts)-1], "/")
}

func baseQuery(sess *session.Session) url.Values {
	return client.BuildCommonQuery(sess.GetJSToken(), sess.GetBDSToken())
}

func precreate(cl *client.Client, sess *session.Session, path string, size int64, contentMD5, sliceMD5 string, mtime int64, blockList []byte) (*precreateResponse, error) {
	form := url.Values{}
	form.Set("path", path)
	form.Set("target_path", parentPath(path))
	form.Set("autoinit", "1")
	form.Set("size", fmt.Sprintf("%d", size))
	form.Set("isdir", "0")
	form.Set("block_list", string(blockList))
	form.Set("rtype", "1")
	form.Set("local_mtime", fmt.Sprintf("%d", mtime))
	form.Set("content-md5", contentMD5)
	form.Set("slice-md5", sliceMD5)
	form.Set("file_limit_switch_v34", "true")

	q := baseQuery(sess)
	q.Set("dp-logid", fmt.Sprintf("%d0001", time.Now().UnixMilli()))
	u := client.BuildAPIURL(cl.BaseURL(), "/api/precreate", q)

	resp, err := cl.DoForm("POST", u, form, map[string]string{
		"Accept":             "application/json, text/plain, */*",
		"Origin":             cl.BaseURL(),
		"Referer":            cl.BaseURL() + "/main?category=all&path=%2F",
		"X-Requested-With":   "XMLHttpRequest",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 405 {
		// fallback to GET
		return precreateViaGet(cl, sess, u, form)
	}
	var pc precreateResponse
	if err := json.Unmarshal(body, &pc); err != nil {
		return nil, fmt.Errorf("parse precreate: %w", err)
	}
	if pc.Errno != 0 {
		return nil, fmt.Errorf("precreate error errno=%d body=%s", pc.Errno, string(body))
	}
	return &pc, nil
}

func precreateViaGet(cl *client.Client, sess *session.Session, u string, form url.Values) (*precreateResponse, error) {
	qu := u + "&" + form.Encode()
	resp, err := cl.DoJSON("GET", qu, nil, map[string]string{
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
	var pc precreateResponse
	if err := json.Unmarshal(body, &pc); err != nil {
		return nil, fmt.Errorf("parse precreate GET: %w", err)
	}
	if pc.Errno != 0 {
		return nil, fmt.Errorf("precreate GET error errno=%d", pc.Errno)
	}
	return &pc, nil
}

func locateUploadHosts(cl *client.Client, sess *session.Session, path, uploadID string) ([]string, error) {
	q := url.Values{}
	q.Set("app_id", appID)
	q.Set("web", "1")
	q.Set("channel", "dubox")
	q.Set("clienttype", "0")
	q.Set("method", "locateupload")
	q.Set("path", path)
	q.Set("uploadid", uploadID)

	var hosts []string
	for _, host := range []string{cl.BaseURL(), "https://dm.nephobox.com"} {
		u := client.BuildAPIURL(host, "/rest/2.0/pcs/file", q)
		resp, err := cl.DoJSON("GET", u, nil, map[string]string{
			"Accept":  "application/json, text/plain, */*",
			"Referer": cl.BaseURL() + "/main?category=all",
		})
		if err != nil {
			continue
		}
		var loc struct {
			Servers []any `json:"servers"`
			Host    string `json:"host"`
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		json.Unmarshal(body, &loc)
		for _, s := range loc.Servers {
			if str, ok := s.(string); ok && str != "" {
				hosts = append(hosts, normalizeHost(str))
			}
		}
		if loc.Host != "" {
			hosts = append(hosts, normalizeHost(loc.Host))
		}
		if len(hosts) > 0 {
			break
		}
	}
	return hosts, nil
}

func buildFallbackHosts(baseURL string) []string {
	domain := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	domain = strings.TrimPrefix(domain, "dm.")
	domain = strings.TrimPrefix(domain, "www.")
	return []string{
		"https://c-all." + domain,
		"https://c-jp." + domain,
		"https://c." + domain,
	}
}

func normalizeHost(h string) string {
	h = strings.TrimRight(strings.TrimSpace(h), "/")
	if !strings.HasPrefix(h, "http") {
		h = "https://" + h
	}
	return h
}

func uploadChunks(cl *client.Client, sess *session.Session, hosts []string, path, uploadID string, data []byte, chunks []struct{ MD5 string }) ([]string, error) {
	uploaded := make([]string, 0, len(chunks))
	for idx := range chunks {
		start := idx * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		partData := data[start:end]

		var lastErr error
		ok := false
		for _, host := range hosts {
			md5res, err := uploadChunk(cl, sess, host, path, uploadID, idx, partData)
			if err != nil {
				lastErr = err
				continue
			}
			uploaded = append(uploaded, md5res)
			ok = true
			break
		}
		if !ok {
			return nil, fmt.Errorf("upload chunk %d failed: %w", idx, lastErr)
		}
	}
	return uploaded, nil
}

func uploadChunk(cl *client.Client, sess *session.Session, host, path, uploadID string, partseq int, data []byte) (string, error) {
	q := url.Values{}
	q.Set("method", "upload")
	q.Set("app_id", appID)
	q.Set("web", "1")
	q.Set("channel", "dubox")
	q.Set("clienttype", "0")
	q.Set("path", path)
	q.Set("uploadid", uploadID)
	q.Set("partseq", fmt.Sprintf("%d", partseq))
	q.Set("type", "tmpfile")
	q.Set("uploadsign", "0")

	u := client.BuildAPIURL(host, "/rest/2.0/pcs/superfile2", q)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	mw.Close()

	req, err := http.NewRequest("POST", u, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", client.UserAgent)
	req.Header.Set("Cookie", cl.CookieHeader())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", cl.BaseURL())
	req.Header.Set("Referer", cl.BaseURL()+"/main?category=all")
	req.Header.Set("Accept", "*/*")

	// Use raw transport to avoid redirect handling complications
	resp, err := cl.Raw(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("chunk upload HTTP %d: %s", resp.StatusCode, string(body))
	}
	var cr chunkUploadResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("parse chunk upload: %w", err)
	}
	if cr.Errno != 0 || cr.ErrorNo != 0 {
		return "", fmt.Errorf("chunk upload error errno=%d error_code=%d msg=%s", cr.Errno, cr.ErrorNo, cr.ErrorMsg)
	}
	if cr.MD5 != "" {
		return cr.MD5, nil
	}
	return md5Hex(data), nil
}

func create(cl *client.Client, sess *session.Session, path string, size int64, contentMD5, sliceMD5 string, mtime int64, blockList []byte, uploadID string) (string, error) {
	form := url.Values{}
	form.Set("path", path)
	form.Set("target_path", parentPath(path))
	form.Set("size", fmt.Sprintf("%d", size))
	form.Set("isdir", "0")
	form.Set("uploadid", uploadID)
	form.Set("block_list", string(blockList))
	form.Set("local_mtime", fmt.Sprintf("%d", mtime))
	form.Set("rtype", "1")
	form.Set("content-md5", contentMD5)
	form.Set("slice-md5", sliceMD5)

	q := baseQuery(sess)
	q.Set("dp-logid", fmt.Sprintf("%d0003", time.Now().UnixMilli()))
	u := client.BuildAPIURL(cl.BaseURL(), "/api/create", q)

	resp, err := cl.DoForm("POST", u, form, map[string]string{
		"Accept":           "application/json, text/plain, */*",
		"Origin":           cl.BaseURL(),
		"Referer":          cl.BaseURL() + "/main?category=all&path=%2F",
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == 405 {
		return createViaGet(cl, sess, u, form)
	}
	var cr createResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("parse create: %w", err)
	}
	if cr.Errno != 0 {
		return "", fmt.Errorf("create error errno=%d body=%s", cr.Errno, string(body))
	}
	return cr.Path, nil
}

func createViaGet(cl *client.Client, sess *session.Session, u string, form url.Values) (string, error) {
	qu := u + "&" + form.Encode()
	resp, err := cl.DoJSON("GET", qu, nil, map[string]string{
		"Referer": cl.BaseURL() + "/main?category=all&path=%2F",
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var cr createResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("parse create GET: %w", err)
	}
	if cr.Errno != 0 {
		return "", fmt.Errorf("create GET error errno=%d", cr.Errno)
	}
	return cr.Path, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
