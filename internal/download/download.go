package download

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mala980/terabox-headless/internal/client"
	"github.com/mala980/terabox-headless/internal/session"
	"github.com/mala980/terabox-headless/internal/terabox"
)

func DownloadFile(cl *client.Client, sess *session.Session, remotePath, localPath string) error {
	dlink, err := terabox.GetDownloadLink(cl, sess, remotePath)
	if err != nil {
		return fmt.Errorf("get download link: %w", err)
	}

	req, err := http.NewRequest("GET", dlink, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", client.UserAgent)
	req.Header.Set("Cookie", cl.CookieHeader())
	req.Header.Set("Referer", cl.BaseURL()+"/main?category=all")

	resp, err := cl.Raw(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	if localPath == "" {
		localPath = extractFilename(resp.Header.Get("Content-Disposition"))
		if localPath == "" {
			localPath = filepath.Base(remotePath)
		}
	}

	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	fmt.Printf("Downloaded %d bytes to %s\n", written, localPath)
	return nil
}

func extractFilename(cd string) string {
	lower := strings.ToLower(cd)
	idx := strings.Index(lower, "filename=")
	if idx < 0 {
		return ""
	}
	val := cd[idx+len("filename="):]
	if strings.HasPrefix(val, `"`) {
		val = val[1:]
		if i := strings.IndexByte(val, '"'); i >= 0 {
			val = val[:i]
		}
	} else if i := strings.IndexByte(val, ';'); i >= 0 {
		val = val[:i]
	}
	return strings.TrimSpace(val)
}
