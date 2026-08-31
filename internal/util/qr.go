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

// RenderQRASCII renders a QR PNG as terminal ASCII art that is scannable.
// It detects the actual module grid from the image rather than sampling
// at a fixed step, so the output stays aligned to module boundaries.
func RenderQRASCII(pngData []byte) error {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return fmt.Errorf("decode PNG: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	brightness := func(x, y int) uint32 {
		r, g, b, _ := img.At(x, y).RGBA()
		return (r + g + b) / 3
	}

	isDark := func(x, y int) bool {
		return brightness(x, y) < 32768
	}

	// Detect the horizontal module size by measuring run lengths in the middle row.
	middleY := height / 2
	runLengths := detectRunLengths(width, middleY, isDark)
	module := mostCommonRun(runLengths)
	if module < 2 {
		module = width / 33
		if module < 1 {
			module = 1
		}
	}

	// Find quiet zone offset: the first column (and row) that is dark after
	// scanning from the top-left border.
	offsetX := findFirstDarkX(width, height, module, isDark)
	offsetY := findFirstDarkY(width, height, module, isDark)
	if offsetX < 0 {
		offsetX = 0
	}
	if offsetY < 0 {
		offsetY = 0
	}

	block := "██"
	empty := "  "

	var sb strings.Builder
	sb.WriteString("\n")
	for y := offsetY; y < height-offsetY; y += module * 2 {
		for x := offsetX; x < width-offsetX; x += module {
			if isDark(x, y) {
				sb.WriteString(block)
			} else {
				sb.WriteString(empty)
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	fmt.Print(sb.String())
	return nil
}

func detectRunLengths(width, y int, isDark func(x, y int) bool) []int {
	var runs []int
	cur := isDark(0, y)
	n := 1
	for x := 1; x < width; x++ {
		if isDark(x, y) == cur {
			n++
			continue
		}
		runs = append(runs, n)
		cur = !cur
		n = 1
	}
	runs = append(runs, n)
	return runs
}

func mostCommonRun(runs []int) int {
	if len(runs) == 0 {
		return 0
	}
	counts := map[int]int{}
	for _, r := range runs {
		counts[r]++
	}
	best, bestCount := runs[0], 0
	for r, c := range counts {
		if c > bestCount {
			best, bestCount = r, c
		}
	}
	return best
}

func findFirstDarkX(width, height, module int, isDark func(x, y int) bool) int {
	for y := module; y < height-module; y += module {
		for x := 0; x < width; x++ {
			if isDark(x, y) {
				return x - module
			}
		}
	}
	return -1
}

func findFirstDarkY(width, height, module int, isDark func(x, y int) bool) int {
	for x := module; x < width-module; x += module {
		for y := 0; y < height; y++ {
			if isDark(x, y) {
				return y - module
			}
		}
	}
	return -1
}

func TryOpenFile(path string) {
	defer func() {
		// Best-effort: never crash the process on platform syscall restrictions.
		_ = recover()
	}()

	candidates := []string{
		"/data/data/com.termux/files/usr/bin/termux-open",
		"/usr/bin/xdg-open",
		"/usr/bin/open",
	}
	for _, tool := range candidates {
		if _, err := os.Stat(tool); err == nil {
			cmd := exec.Command(tool, path)
			cmd.Start()
			return
		}
	}
}
