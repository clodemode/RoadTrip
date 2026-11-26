package main

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	Type        string // og:image:type: image/png for the trip and fallback cards, image/jpeg for a photo card
}

// socialImage is a card a page names in place of the trip card (the photo
// page's own photo card). A zero value names none.
type socialImage struct{ URL, Alt, Type string }

// social builds the tag block for the page at r. With the whole site private
// the title and description are generic and name no ride.
//
// slug names the ride whose trip card the page shows; empty (the sign-in page,
// any page with no ride) names the fallback card, as does a private site and a
// ride that cannot be drawn.
func (a *App) social(r *http.Request, slug, rideName, kind, title, desc string, own ...socialImage) socialMeta {
	if a.siteURL == "" {
		return socialMeta{}
	}
	m := socialMeta{
		On:    true,
		URL:   a.siteURL + r.URL.Path,
		Image: a.siteURL + socialCardPath + "?v=" + a.assetV,
		Alt:   socialCardAlt,
		Type:  "image/png",
	}
	if a.sitePrivate {
		m.SiteName, m.Title, m.Description = socialSite, socialSite, socialLine
		return m
	}
	if img, alt, ok := a.cardAddress(slug); ok {
		m.Image, m.Alt = img, alt
	}
	if len(own) > 0 && own[0].URL != "" {
		m.Image, m.Alt, m.Type = own[0].URL, own[0].Alt, own[0].Type
	}
	m.SiteName = rideName
	m.Title = rideName
	if title != "" {
		m.Title = title + " — " + rideName
	}
	m.Description = desc
	if desc == "" {
		m.Description = fmt.Sprintf("%s: %s, live from the road.", rideName, kind)
	}
	return m
}

// cutWords flattens s to one line and, if it is longer than max characters,
// cuts it at a word with an ellipsis so the result is never longer than max.
func cutWords(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	r = r[:max-1]
	if i := strings.LastIndex(string(r), " "); i > 0 {
		r = []rune(string(r)[:i])
	}
	return strings.TrimRight(string(r), " ,;:.-—") + "…"
}

// planFigures are the numbers the public Plan page shows for a ride: miles,
// days and the first and last date. ok is false when there is no plan to read.
func (a *App) planFigures(slug string) (miles, days int, start, end string, ok bool) {
	st, err := a.loadPlanSlug(slug)
	if err != nil {
		return 0, 0, "", "", false
	}
	choices, from, _ := st.requested(url.Values{}, false)
	sum := st.Doc.summarize(choices, from)
	return int(math.Round(sum.TotalMiles)), sum.DayCount, sum.StartDate, sum.EndDate, true
}

// figuresPhrase writes "1,234 miles over 4 days, Fri May 1 to Mon May 4, 2030",
// leaving out whatever is not known.
func figuresPhrase(miles, days int, start, end string) string {
	var parts []string
	if miles > 0 {
		t := thousands(miles) + " miles"
		if days > 0 {
			t += " over " + strconv.Itoa(days) + " day" + map[bool]string{true: "", false: "s"}[days == 1]
		}
		parts = append(parts, t)
	}
	if d := cardDates(start, end); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, ", ")
}
