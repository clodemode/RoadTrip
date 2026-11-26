package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// Steps for features/masthead_contrast.feature. The contrast of a control is
// computed from static/css/app.css: its rule's ink against its rule's
// background, or against every stop of the masthead's gradient when the rule
// sets none (a control is judged on its worst stop). A translucent
// background is laid over each stop first. relLuminance and contrastRatio are
// drop_pin_steps_test.go's.

type cssRule struct {
	selectors []string
	body      string
}

func parseCSS(css string) []cssRule {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	var out []cssRule
	// A flat scan: every `selectors { declarations }` with no nested braces,
	// at any @media depth. A rule inside @media still counts: the narrower
	// one only restates the same colours.
	for _, m := range regexp.MustCompile(`([^{}@]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		var sels []string
		for _, s := range strings.Split(m[1], ",") {
			sels = append(sels, strings.Join(strings.Fields(s), " "))
		}
		out = append(out, cssRule{sels, m[2]})
	}
	return out
}

func (r cssRule) prop(name string) string {
	m := regexp.MustCompile(`(?:^|[;\s])` + regexp.QuoteMeta(name) + `:\s*([^;]+)`).FindStringSubmatch(r.body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

type rgba struct{ r, g, b, a float64 }

func (c rgba) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round(c.r)), int(math.Round(c.g)), int(math.Round(c.b)))
}

func parseColour(v string, vars map[string]string) (rgba, error) {
	v = strings.TrimSpace(v)
	if m := regexp.MustCompile(`^var\((--[a-z-]+)\)$`).FindStringSubmatch(v); m != nil {
		inner, ok := vars[m[1]]
		if !ok {
			return rgba{}, fmt.Errorf("no custom property %s", m[1])
		}
		return parseColour(inner, vars)
	}
	if m := regexp.MustCompile(`^#([0-9a-fA-F]{3})$`).FindStringSubmatch(v); m != nil {
		v = "#" + string([]byte{m[1][0], m[1][0], m[1][1], m[1][1], m[1][2], m[1][2]})
	}
	if m := regexp.MustCompile(`^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$`).FindStringSubmatch(v); m != nil {
		var c [3]float64
		for i := range c {
			n, _ := strconv.ParseUint(m[i+1], 16, 8)
			c[i] = float64(n)
		}
		return rgba{c[0], c[1], c[2], 1}, nil
	}
	if m := regexp.MustCompile(`^rgba?\(\s*(\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\s*\)$`).FindStringSubmatch(v); m != nil {
		r, _ := strconv.ParseFloat(m[1], 64)
		g, _ := strconv.ParseFloat(m[2], 64)
		b, _ := strconv.ParseFloat(m[3], 64)
		a := 1.0
		if m[4] != "" {
			a, _ = strconv.ParseFloat(m[4], 64)
		}
		return rgba{r, g, b, a}, nil
	}
	return rgba{}, fmt.Errorf("cannot read the colour %q", v)
}

func over(top, under rgba) rgba {
	return rgba{top.r*top.a + under.r*(1-top.a), top.g*top.a + under.g*(1-top.a), top.b*top.a + under.b*(1-top.a), 1}
}

// firstColour is the first colour word of a background value, so a gradient or
// a shorthand with an image is refused rather than guessed.
func bgColour(v string) string {
	if v == "" || strings.Contains(v, "gradient") || strings.Contains(v, "url(") {
		return ""
	}
	return v
}

type contrastWorld struct {
	css   string
	rules []cssRule
	vars  map[string]string
	tmpl  string
}

func (w *contrastWorld) load() error {
	b, err := os.ReadFile("static/css/app.css")
	if err != nil {
		return err
	}
	w.css = string(b)
	w.rules = parseCSS(w.css)
	w.vars = map[string]string{}
	for _, m := range regexp.MustCompile(`(--[a-z-]+):\s*([^;]+);`).FindAllStringSubmatch(w.css, -1) {
		if _, dup := w.vars[m[1]]; !dup {
			w.vars[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return nil
}

// rule is the cascade for one exact selector, last declaration of a property
// winning, the way the stylesheet orders them.
func (w *contrastWorld) prop(selector, name string) string {
	val := ""
	for _, r := range w.rules {
		for _, s := range r.selectors {
			if s == selector {
				if v := r.prop(name); v != "" {
					val = v
				}
			}
		}
	}
	return val
}

func (w *contrastWorld) band() ([]rgba, error) {
	var out []rgba
	for _, k := range []string{"--rt-mast-from", "--rt-mast-mid", "--rt-mast-to"} {
		c, err := parseColour("var("+k+")", w.vars)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (w *contrastWorld) worst(selector, inkFrom string) (float64, string, error) {
	inkVal := w.prop(selector, "color")
	if inkVal == "" {
		inkVal = w.prop(inkFrom, "color")
	}
	if inkVal == "" {
		return 0, "", fmt.Errorf("neither %q nor %q sets a colour", selector, inkFrom)
	}
	ink, err := parseColour(inkVal, w.vars)
	if err != nil {
		return 0, "", err
	}
	bgVal := bgColour(w.prop(selector, "background"))
	if bgVal == "" {
		bgVal = bgColour(w.prop(selector, "background-color"))
	}
	var unders []rgba
	if bgVal == "" {
		if unders, err = w.band(); err != nil {
			return 0, "", err
		}
	} else {
		bg, err := parseColour(bgVal, w.vars)
		if err != nil {
			return 0, "", err
		}
		if bg.a < 1 {
			band, err := w.band()
			if err != nil {
				return 0, "", err
			}
			for _, b := range band {
				unders = append(unders, over(bg, b))
			}
		} else {
			unders = []rgba{bg}
		}
	}
	min, at := math.Inf(1), ""
	for _, u := range unders {
		if r := contrastRatio(ink.hex(), u.hex()); r < min {
			min, at = r, u.hex()
		}
	}
	return min, at + " with ink " + ink.hex(), nil
}

// mastheadKey is the contrast-list selector an element of the masthead is
// covered by; "" for an element the list does not know.
func mastheadKey(tag, class string, inMenu bool) string {
	switch {
	case strings.Contains(class, "mast-brand"):
		return ".mast-title"
	case strings.Contains(class, "drop-pin"):
		return ".mast-links .drop-pin"
	case strings.Contains(class, "linkish"):
		return ".mast-links .linkish"
	case strings.Contains(class, "clode-badge"):
		return ".clode-badge"
	case tag == "a" && inMenu && (class == "" || class == "post-link"):
		return ".mast-links a"
	}
	return ""
}

func initMastheadContrastScenarios(sc *godog.ScenarioContext) {
	w := &contrastWorld{}
	sc.Step(`^the stylesheet is read$`, w.load)
	sc.Step(`^the "([^"]*)" rule "(.+)" reads at 7 to 1 or better, taking its ink from "([^"]*)" when it sets none$`,
		func(control, selector, inkFrom string) error {
			r, at, err := w.worst(selector, inkFrom)
			if err != nil {
				return fmt.Errorf("%s: %v", control, err)
			}
			if r < 7 {
				return fmt.Errorf("%s (%s) reads %.2f to 1 on its worst background %s, want at least 7", control, selector, r, at)
			}
			return nil
		})
	sc.Step(`^the masthead's template$`, func() error {
		b, err := os.ReadFile("templates/base.html")
		w.tmpl = string(b)
		return err
	})
	sc.Step(`^every link and button in the masthead is one the contrast list names$`, func() error {
		feat, err := os.ReadFile("features/masthead_contrast.feature")
		if err != nil {
			return err
		}
		listed := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^\s*\|[^|]*\|\s*([^|\s][^|]*?)\s*\|[^|]*\|\s*$`).FindAllStringSubmatch(string(feat), -1) {
			listed[m[1]] = true
		}
		i, j := strings.Index(w.tmpl, `<header class="masthead">`), strings.Index(w.tmpl, `</header>`)
		if i < 0 || j < i {
			return fmt.Errorf("base.html has no masthead")
		}
		head := w.tmpl[i:j]
		menuAt := strings.Index(head, `<span class="mast-links">`)
		n := 0
		for _, m := range regexp.MustCompile(`<(a|button)\b([^>]*)>`).FindAllStringSubmatchIndex(head, -1) {
			tag, attrs := head[m[2]:m[3]], head[m[4]:m[5]]
			class := ""
			if c := regexp.MustCompile(`\bclass="([^"]*)"`).FindStringSubmatch(attrs); c != nil {
				class = c[1]
			}
			n++
			key := mastheadKey(tag, class, menuAt >= 0 && m[0] > menuAt)
			if key == "" || !listed[key] {
				return fmt.Errorf("the masthead's <%s%s> is on no contrast list entry (key %q)", tag, attrs, key)
			}
		}
		if n == 0 {
			return fmt.Errorf("found no control in the masthead")
		}
		return nil
	})
}
