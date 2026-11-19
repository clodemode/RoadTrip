package main

import (
	"fmt"
	"net/http"
)

// The link-preview tag block: one partial (templates/social_meta.html), one
// data shape, used by every full document. spec-roadtrip-social-preview-cards-a-live-route-card-per-trip-the-photo-itself-on-a-photo-page-a-baked-odo-card-as-the-fallback-one-tag-block-and-a-check-that-reads-the-page-as-a-scraper-does.

const (
	socialCardPath = "/static/img/og-card.png"
	socialSite     = "Road Trip"
	socialLine     = "Live from the road."
	socialCardAlt  = "Odo, a nine-banded armadillo, standing on a sunlit disc beside the words Road Trip"
)

// socialMeta is what the partial reads as .Social. On is false when SITE_URL
// is unset: no tag is emitted then.
type socialMeta struct {
	On          bool
	SiteName    string
	Title       string
	Description string
	URL         string
	Image       string
	Alt         string
}

// social builds the tag block for the page at r. With the whole site private
// the title and description are generic and name no ride.
//
// slug names the ride whose trip card the page shows; empty (the sign-in page,
// any page with no ride) names the fallback card, as does a private site and a
// ride that cannot be drawn.
func (a *App) social(r *http.Request, slug, rideName, title, desc string) socialMeta {
	if a.siteURL == "" {
		return socialMeta{}
	}
	m := socialMeta{
		On:    true,
		URL:   a.siteURL + r.URL.Path,
		Image: a.siteURL + socialCardPath + "?v=" + a.assetV,
		Alt:   socialCardAlt,
	}
	if a.sitePrivate {
		m.SiteName, m.Title, m.Description = socialSite, socialSite, socialLine
		return m
	}
	if img, alt, ok := a.cardAddress(slug); ok {
		m.Image, m.Alt = img, alt
	}
	m.SiteName = rideName
	m.Title = rideName
	if title != "" {
		m.Title = title + " — " + rideName
	}
	m.Description = desc
	if desc == "" {
		m.Description = fmt.Sprintf("%s: a one-way drive, live from the road.", rideName)
	}
	return m
}
