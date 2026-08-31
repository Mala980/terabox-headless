package util

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func DecodeQRDataURL(dataURL string) ([]byte, error) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return nil, fmt.Errorf("unsupported QR data URL format")
	}
	return base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
}

func DisplayQRToFile(pngData []byte) (string, error) {
	path := filepath.Join(os.TempDir(), "terabox-qr.png")
	if err := os.WriteFile(path, pngData, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func RenderQRASCII(pngData []byte) error {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return fmt.Errorf("decode PNG: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	step := width / 33
	if step < 1 {
		step = 1
	}

	var sb strings.Builder
	sb.WriteString("\n")
	for y := 0; y < height; y += step * 2 {
		for x := 0; x < width; x += step {
			r, g, b, _ := img.At(x, y).RGBA()
			brightness := (r + g + b) / 3
			if brightness < 32768 {
				sb.WriteString("██")
			} else {
				sb.WriteString("  ")
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	fmt.Print(sb.String())
	return nil
}

func TryOpenFile(path string) {
	if _, err := os.Stat("/data/data/com.termux/files/usr/bin/termux-open"); err == nil {
		exec.Command("termux-open", path).Start()
		return
	}
	for _, tool := range []string{"xdg-open", "open"} {
		if _, err := exec.LookPath(tool); err == nil {
			exec.Command(tool, path).Start()
			return
		}
	}
}
