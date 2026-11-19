package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Steps for features/listen_inn_sits_above_the_map.feature. Kris, 2026-11-18:
// the Listen Inn card is part of the page, in the band at the top beside the
// latest tiles, and never over the map. They share the npWorld of
// public_now_playing.feature: the driver reports a track, a visitor reads pages.

// mediaRule is the body of the rule for sel inside the first @media block
// whose condition contains cond, from comment-stripped CSS; empty when none.
func mediaRule(css, cond, sel string) string {
	re := regexp.MustCompile(`@media\s*\(([^)]*)\)\s*\{((?:[^{}]*\{[^}]*\})*)\s*\}`)
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		if strings.Contains(m[1], cond) {
			if r := ruleOf(m[2], sel); r != "" {
				return r
			}
		}
	}
	return ""
}

func initListenInnAboveMapScenarios(t *testing.T, sc *godog.ScenarioContext, np *npWorld) {
	w := &licWorld{npWorld: np}
	band := func(page string) (string, error) {
		m := regexp.MustCompile(`(?s)<div class="top-band">.*?<div id="map"`).FindString(page)
		if m == "" {
			return "", fmt.Errorf("the Ride page has no top band before the map")
		}
		return m, nil
	}
	sc.Step(`^the Ride page carries the Listen Inn card before the map, in the top band$`, func() error {
		page := w.visit("/")
		b, err := band(page)
		if err != nil {
			return err
		}
		if !strings.Contains(b, `<section class="tune-card listen-card listen-top" id="np"`) {
			return fmt.Errorf("the top band holds no Listen Inn card")
		}
		if n := strings.Count(page, `id="np"`); n != 1 {
			return fmt.Errorf("the page holds the card %d times", n)
		}
		return nil
	})
	sc.Step(`^no style rule fixes the Listen Inn card or floats it over the page$`, func() error {
		css := appCSS(t)
		if strings.Contains(css, "listen-dock") {
			return fmt.Errorf("the retired class listen-dock is still styled")
		}
		for _, m := range regexp.MustCompile(`([^{}]+)\{([^}]*)\}`).FindAllStringSubmatch(css, -1) {
			if !strings.Contains(m[1], "listen") && !strings.Contains(m[1], "#np") && !strings.Contains(m[1], "top-band") {
				continue
			}
			for _, bad := range []string{"position: fixed", "position: sticky", "position:fixed", "z-index"} {
				if strings.Contains(m[2], bad) {
					return fmt.Errorf("the rule %q holds %q", strings.TrimSpace(m[1]), bad)
				}
			}
		}
		return nil
	})
	sc.Step(`^the page reserves no padding at its foot for a docked card$`, func() error {
		if regexp.MustCompile(`body:has\([^)]*listen[^)]*\)`).MatchString(appCSS(t)) {
			return fmt.Errorf("the page still reserves room for a docked card")
		}
		return nil
	})
	sc.Step(`^from 768 px the top band is one row and the card is no wider than 24rem$`, func() error {
		css := appCSS(t)
		row := mediaRule(css, "min-width: 768px", ".top-band")
		if !strings.Contains(row, "flex-direction: row") {
			return fmt.Errorf("from 768 px the band is not a row: %q", row)
		}
		card := mediaRule(css, "min-width: 768px", ".top-band .listen-top")
		if !strings.Contains(card, "max-width: 24rem") {
			return fmt.Errorf("from 768 px the card is not capped at 24rem: %q", card)
		}
		return nil
	})
	sc.Step(`^under 768 px the top band stacks and the card is full width with a 16px gutter and no sideways scroll$`, func() error {
		css := appCSS(t)
		if b := ruleOf(css, ".top-band"); !strings.Contains(b, "flex-direction: column") {
			return fmt.Errorf("the band does not stack on a phone: %q", b)
		}
		c := ruleOf(css, ".listen-top")
		if !strings.Contains(c, "width: 100%") || strings.Contains(c, "min-width") {
			return fmt.Errorf("the card is not full width: %q", c)
		}
		if !strings.Contains(ruleOf(css, ".listen-card"), "box-sizing: border-box") && !strings.Contains(c, "box-sizing: border-box") {
			return fmt.Errorf("the card's border can push it past the gutter")
		}
		if !regexp.MustCompile(`main\s*\{[^}]*padding:\s*0 1rem`).MatchString(css) {
			return fmt.Errorf("the page has no 16px gutter at its side")
		}
		return nil
	})
	sc.Step(`^the closed card holds its title button and one line and is at most 72px tall$`, func() error {
		page := w.visit("/")
		b, err := band(page)
		if err != nil {
			return err
		}
		if !strings.Contains(b, `aria-expanded="false"`) || !strings.Contains(b, `id="np-chip"`) || !strings.Contains(b, `id="np-body" hidden`) {
			return fmt.Errorf("the closed card is not a title and one line")
		}
		r := ruleOf(appCSS(t), `.listen-top:has(#np-toggle[aria-expanded="false"])`)
		m := regexp.MustCompile(`max-height:\s*(\d+)px`).FindStringSubmatch(r)
		if m == nil {
			return fmt.Errorf("nothing caps the closed card's height: %q", r)
		}
		if n, _ := strconv.Atoi(m[1]); n > 72 {
			return fmt.Errorf("the closed card may be %dpx tall", n)
		}
		return nil
	})
	sc.Step(`^the open list scrolls inside itself and takes its own room in the page$`, func() error {
		css := appCSS(t)
		p := ruleOf(css, ".np-panel")
		if !strings.Contains(p, "overflow-y: auto") || !strings.Contains(p, "max-height") {
			return fmt.Errorf("the list does not scroll inside itself: %q", p)
		}
		for _, sel := range []string{".listen-top", ".top-band"} {
			if r := ruleOf(css, sel); strings.Contains(r, "position: absolute") || strings.Contains(r, "position: fixed") {
				return fmt.Errorf("%s is taken out of the flow: %q", sel, r)
			}
		}
		return nil
	})
	sc.Step(`^the Ride page for a visitor draws no song tile in the latest strip$`, func() error {
		if page := w.visit("/"); strings.Contains(page, "latest-song") {
			return fmt.Errorf("the strip still draws the Last played tile")
		}
		return nil
	})
	sc.Step(`^the Ride page inside the driver's player frame still draws the song tile$`, func() error {
		page := doAs(t, w.app, w.kris, "GET", "/?embed=1", nil).Body.String()
		if !strings.Contains(page, "latest-song") {
			return fmt.Errorf("the framed page has no song tile")
		}
		if strings.Contains(page, `id="np"`) {
			return fmt.Errorf("the framed page holds a Listen Inn card")
		}
		return nil
	})
	sc.Step(`^the Ride page for a visitor holds one audio element and one Play button$`, func() error {
		page := w.visit("/")
		if n := strings.Count(page, "<audio"); n != 1 {
			return fmt.Errorf("%d audio elements", n)
		}
		if n := strings.Count(page, `id="np-listen"`); n != 1 {
			return fmt.Errorf("%d Play buttons", n)
		}
		return nil
	})
	sc.Step(`^the song link of "([^"]*)" renders the card in the top band, open$`, func(title string) error {
		tr, err := w.npWorld.track(title)
		if err != nil {
			return err
		}
		b, err := band(w.visit("/listeninn/song/" + tr.ID))
		if err != nil {
			return err
		}
		if !strings.Contains(b, `id="np"`) || !strings.Contains(b, `aria-expanded="true"`) {
			return fmt.Errorf("the song link's card is not open in the top band")
		}
		return nil
	})
}
