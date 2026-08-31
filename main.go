package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mala980/terabox-headless/internal/client"
	"github.com/mala980/terabox-headless/internal/download"
	"github.com/mala980/terabox-headless/internal/emaillogin"
	"github.com/mala980/terabox-headless/internal/qrlogin"
	"github.com/mala980/terabox-headless/internal/session"
	"github.com/mala980/terabox-headless/internal/terabox"
	"github.com/mala980/terabox-headless/internal/upload"
	"github.com/mala980/terabox-headless/internal/util"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	cmd := os.Args[1]

	sess, err := session.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading session: %v\n", err)
		os.Exit(1)
	}

	baseURL := sess.GetBaseURL()
	cl := client.New(baseURL)
	if sess.LoggedIn() {
		cl.SetCookies(sess.GetCookies())
		if sess.GetJSToken() == "" {
			refreshSession(cl, sess)
		}
	} else {
		cl.SetCookies(sess.GetCookies())
	}

	switch cmd {
	case "login":
		cmdLogin(cl, sess, os.Args[2:])
	case "logout":
		cmdLogout(sess)
	case "status":
		cmdStatus(cl, sess)
	case "ls":
		dir := "/"
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		cmdList(cl, sess, dir)
	case "upload":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: terabox upload <local-file> [remote-dir]")
			os.Exit(1)
		}
		remoteDir := ""
		if len(os.Args) > 3 {
			remoteDir = os.Args[3]
		}
		cmdUpload(cl, sess, os.Args[2], remoteDir)
	case "dl", "download":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: terabox dl <remote-path> [local-path]")
			os.Exit(1)
		}
		localPath := ""
		if len(os.Args) > 3 {
			localPath = os.Args[3]
		}
		cmdDownload(cl, sess, os.Args[2], localPath)
	default:
		usage()
	}
}

func usage() {
	fmt.Println(`Terabox Headless Browser - lightweight CLI for terabox.com
Usage:
  terabox login              Start QR login and save session
  terabox login <email>      Login with email/password
  terabox logout             Clear saved session
  terabox status             Check login status and quota
  terabox ls [dir]           List files in directory
  terabox upload <file> [dir]  Upload file to remote directory
  terabox dl <remote> [local]  Download file from remote path`)
}

func cmdLogin(cl *client.Client, sess *session.Session, args []string) {
	if sess.LoggedIn() {
		fmt.Print("Already logged in. Login again? (y/N): ")
		var resp string
		fmt.Scanln(&resp)
		if strings.ToLower(resp) != "y" {
			return
		}
	}

	if len(args) > 0 {
		email := args[0]
		if !strings.Contains(email, "@") {
			fmt.Fprintf(os.Stderr, "Usage: terabox login <email>  (QR login: terabox login)\n")
			os.Exit(1)
		}
		fmt.Print("Password: ")
		var password string
		fmt.Scanln(&password)
		if password == "" {
			password = os.Getenv("TERABOX_PASSWORD")
		}
		if password == "" {
			fmt.Fprintln(os.Stderr, "Password required. Set TERABOX_PASSWORD env var or enter it.")
			os.Exit(1)
		}
		fmt.Println("Logging in with email...")
		if err := emaillogin.EmailLogin(cl, sess, email, password); err != nil {
			fmt.Fprintf(os.Stderr, "Email login failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Login complete. Session saved.")
		return
	}

	start, err := qrlogin.Start(cl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "QR login start failed: %v\n", err)
		os.Exit(1)
	}

	qrPath, err := util.DisplayQRToFile(start.PngData)
	if err == nil {
		fmt.Printf("QR code saved to: %s\n", qrPath)
		util.TryOpenFile(qrPath)
	}

	fmt.Println("\nScan the QR code with the TeraBox mobile app...")
	util.RenderQRASCII(start.PngData)

	result, err := qrlogin.Poll(cl, start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "QR login failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("QR login successful! Account: %s\n", result.UserName)

	if err := qrlogin.CompleteLoginWithNDUS(cl, sess, result.NDUS); err != nil {
		fmt.Fprintf(os.Stderr, "Session setup failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Login complete. Session saved.")
}

func cmdLogout(sess *session.Session) {
	if err := sess.Delete(); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing session: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Logged out. Session cleared.")
}

func cmdStatus(cl *client.Client, sess *session.Session) {
	if !sess.LoggedIn() {
		fmt.Println("Not logged in. Run 'terabox login' first.")
		return
	}

	info, err := terabox.GetLoginInfo(cl, sess.GetJSToken())
	if err != nil {
		refreshSession(cl, sess)
		info, err = terabox.GetLoginInfo(cl, sess.GetJSToken())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Status check failed: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("Logged in: %s\n", info.Data.UserName)

	quota, err := terabox.GetQuota(cl, sess.GetJSToken())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Quota check failed: %v\n", err)
		return
	}
	if quota.Data != nil {
		fmt.Printf("Storage: %s / %s\n", formatBytes(quota.Data.Used), formatBytes(quota.Data.Total))
	}
}

func cmdList(cl *client.Client, sess *session.Session, dir string) {
	if !sess.LoggedIn() {
		fmt.Println("Not logged in.")
		return
	}

	list, err := terabox.ListFiles(cl, sess, dir, 100)
	if err != nil {
		refreshSession(cl, sess)
		list, err = terabox.ListFiles(cl, sess, dir, 100)
		if err != nil {
			fmt.Fprintf(os.Stderr, "List failed: %v\n", err)
			os.Exit(1)
		}
	}

	if len(list.List) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, f := range list.List {
		typ := "F"
		if f.IsDir == 1 {
			typ = "D"
		}
		fmt.Printf("[%s] %s (%s)\n", typ, f.ServerName, formatBytes(f.Size))
	}
}

func cmdUpload(cl *client.Client, sess *session.Session, localPath, remoteDir string) {
	if !sess.LoggedIn() {
		fmt.Println("Not logged in. Run 'terabox login' first.")
		os.Exit(1)
	}

	if remoteDir == "" {
		remoteDir = "/"
	}

	fmt.Printf("Uploading %s to %s...\n", localPath, remoteDir)
	path, err := upload.UploadFile(cl, sess, localPath, remoteDir)
	if err != nil {
		refreshSession(cl, sess)
		path, err = upload.UploadFile(cl, sess, localPath, remoteDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Upload failed: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("Uploaded to: %s\n", path)
}

func cmdDownload(cl *client.Client, sess *session.Session, remotePath, localPath string) {
	if !sess.LoggedIn() {
		fmt.Println("Not logged in. Run 'terabox login' first.")
		os.Exit(1)
	}

	fmt.Printf("Downloading %s...\n", remotePath)
	err := download.DownloadFile(cl, sess, remotePath, localPath)
	if err != nil {
		refreshSession(cl, sess)
		err = download.DownloadFile(cl, sess, remotePath, localPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
			os.Exit(1)
		}
	}
}

func refreshSession(cl *client.Client, sess *session.Session) {
	if !sess.LoggedIn() {
		return
	}
	resp, err := cl.Do("GET", sess.GetBaseURL()+"/main?category=all&path=%2F", nil, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return
	}
	result := sess.RefreshFromMainPage(string(body), resp.Header.Values("Set-Cookie"), resp.Request.URL.String())
	cl.SetCookies(result.Cookies)
	if result.BaseURL != "" {
		cl.SetBaseURL(result.BaseURL)
	}
	sess.Save()
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}