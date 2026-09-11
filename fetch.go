package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxDocBytes = 100 << 20 // 100 MiB

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// openURL downloads u into dataDir/docs; PDFs also get a pdftotext copy.
func openURL(ctx context.Context, dataDir, u string) (*Doc, error) {
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("url must start with http:// or https://")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; reader/1.0)")
	req.Header.Set("Accept", "application/pdf,text/html;q=0.9,*/*;q=0.8")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("fetch failed: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDocBytes {
		return nil, errors.New("document larger than 100 MiB")
	}

	id := newID()
	doc := &Doc{ID: id, URL: u, Opened: time.Now()}
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "application/pdf") || bytes.HasPrefix(body, []byte("%PDF")):
		doc.Kind = "pdf"
		doc.File = filepath.Join(dataDir, "docs", id+".pdf")
		doc.TextFile = filepath.Join(dataDir, "docs", id+".txt")
		if err := os.WriteFile(doc.File, body, 0o644); err != nil {
			return nil, err
		}
		if err := pdfToText(ctx, doc.File, doc.TextFile); err != nil {
			return nil, fmt.Errorf("pdftotext: %w", err)
		}
		doc.Title = pdfTitle(ctx, doc.File, parsed)
	default:
		doc.Kind = "html"
		doc.File = filepath.Join(dataDir, "docs", id+".html")
		if err := os.WriteFile(doc.File, body, 0o644); err != nil {
			return nil, err
		}
		doc.Title = htmlTitle(body, parsed)
	}
	return doc, nil
}

func pdfToText(ctx context.Context, in, out string) error {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", in, out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// pdfTitle uses pdfinfo metadata, else the most title-cased early line of page one.
func pdfTitle(ctx context.Context, pdf string, u *url.URL) string {
	if out, err := exec.CommandContext(ctx, "pdfinfo", pdf).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if t, ok := strings.CutPrefix(line, "Title:"); ok {
				if t = strings.TrimSpace(t); t != "" {
					return t
				}
			}
		}
	}
	if out, err := exec.CommandContext(ctx, "pdftotext", "-l", "1", pdf, "-").Output(); err == nil {
		best, bestScore, n := "", 0.0, 0
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.Join(strings.Fields(line), " ")
			if line == "" {
				continue
			}
			if n++; n > 10 {
				break
			}
			if len(line) < 8 || len(line) > 150 || strings.HasPrefix(line, "arXiv:") {
				continue
			}
			words := strings.Fields(line)
			caps := 0
			for _, w := range words {
				if r := rune(w[0]); r >= 'A' && r <= 'Z' {
					caps++
				}
			}
			if score := float64(caps) / float64(len(words)); score > bestScore {
				best, bestScore = line, score
			}
		}
		if best != "" {
			return best
		}
	}
	if base := filepath.Base(u.Path); base != "" && base != "/" {
		return base
	}
	return u.Host
}

func htmlTitle(body []byte, u *url.URL) string {
	if m := titleRe.FindSubmatch(body); m != nil {
		t := strings.TrimSpace(html.UnescapeString(string(m[1])))
		t = strings.Join(strings.Fields(t), " ")
		if t != "" {
			return t
		}
	}
	return u.Host + u.Path
}
