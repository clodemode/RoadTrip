package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The fallback card and the tag block. spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.

func TestSocialFallbackCardIsA1200x630PNGUnder300KB(t *testing.T) {
	b, err := os.ReadFile("static/img/og-card.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 300*1024 {
		t.Errorf("og-card.png is %d bytes, want under 307200", len(b))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if cfg.Width != 1200 || cfg.Height != 630 {
		t.Errorf("og-card.png is %dx%d, want 1200x630", cfg.Width, cfg.Height)
	}
}

// Every template that is a whole document (has its own <html>) takes the one
// tag block; found by reading the templates, not by a list of names.
func TestEveryFullDocumentTemplateIncludesTheSocialBlock(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	docs, defs := 0, 0
	for _, f := range files {
		b, _ := os.ReadFile(f)
		s := string(b)
		if strings.Contains(s, `{{define "social_meta"}}`) {
			defs++
		}
		if !regexp.MustCompile(`(?i)<html[\s>]`).MatchString(s) {
			continue
		}
		docs++
		if !strings.Contains(s, `{{template "social_meta" .}}`) {
			t.Errorf("%s is a full document and does not include social_meta", f)
		}
	}
	if docs < 2 {
		t.Errorf("found %d full-document templates, want at least 2", docs)
	}
	if defs != 1 {
		t.Errorf("social_meta is defined %d times, want once", defs)
	}
}
