package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Chester and podcasts. Chester can search Apple's free podcast search (no key)
// and recommend one episode as a card that opens in Apple Podcasts. Kris,
// 2026-11-12: a podcast that fits the place, from a publisher worth hearing,
// not the first hit.
//
// The model never sees an address. search_podcasts answers with each kept
// episode's show, episode, year, minutes and a short reference (p1, p2, ...)
// that is good for this one question only. show_podcast takes a reference back
// and the server builds the card from the address its own search returned, so
// the model cannot invent an episode or a link. The search term is words only:
// a coordinate or anything private is never sent.

const (
	chesterSearchPodcastsTool = "search_podcasts"
	chesterShowPodcastTool    = "show_podcast"
	chesterPodcastResults     = 8   // episodes one search hands the model
	chesterPodcastSearches    = 5   // searches one answer may run
	chesterPodcastMinMinutes  = 15  // shorter is a clip, not a listen
	chesterPodcastMaxMinutes  = 120 // longer is a sleep track or a marathon
	chesterPodcastTimeout     = 6 * time.Second
	chesterPodcastLabel       = "Open in Apple Podcasts"

	chesterPodcastDown = "The podcast search is unavailable right now. Tell the traveller so plainly, and do not suggest an episode from memory."
)

const chesterPodcastPrompt = `

Podcasts. When the traveller asks for a podcast, or to learn about where the car is, call search_podcasts with words only (a place, its history, its rivers, its people). Search several times with different words, as separate calls. Prefer established publishers (public radio, universities, museums, the park service, well-known history shows) over thin machine-made shows, and judge by publisher, length and how well the episode fits the place, never by the first result. Recommend only episodes the searches return. Then call show_podcast once with the reference of the one episode you choose and one short sentence on why it fits where the car is. The page turns it into a card with an Open in Apple Podcasts button; you cannot play anything yourself, so say that they tap it to open the episode. If search_podcasts says the search is unavailable, say so plainly and do not suggest an episode. Write the answer after your last tool call, as for places.`

// podcastSvc is Apple's podcast search. nil is the feature off.
type podcastSvc struct {
	base   string
	client *http.Client
}

func newPodcastSvc() *podcastSvc {
	return &podcastSvc{base: "https://itunes.apple.com", client: &http.Client{}}
}

// podcastEp is one kept episode.
type podcastEp struct {
	Show, Episode, Artist, URL string
	Year, Minutes              int
}

// chesterPodcastLib is the search as one question sees it.
type chesterPodcastLib struct {
	svc  *podcastSvc
	melt *meltSink
}

type chesterPodcastCard struct {
	Show    string `json:"show"`
	Episode string `json:"episode"`
	Minutes int    `json:"minutes"`
	Year    int    `json:"year,omitempty"`
	Why     string `json:"why"`
	Href    string `json:"href"`
	Label   string `json:"label"`
}

var (
	podcastDecimal = regexp.MustCompile(`-?\d+\.\d+`)
	podcastLong    = regexp.MustCompile(`\d{5,}`)
)

// podcastTerm is the words that may be sent: a token that looks like a
// coordinate or a long number is dropped.
func podcastTerm(q string) string {
	var out []string
	for _, w := range strings.Fields(podcastDecimal.ReplaceAllString(cleanLine(q, 80), " ")) {
		if w = strings.Trim(w, ",;"); w == "" || podcastLong.MatchString(w) {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// appleHost is whether u is an https address on apple.com.
func appleHost(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" {
		return false
	}
	h := strings.ToLower(p.Hostname())
	return h == "apple.com" || strings.HasSuffix(h, ".apple.com")
}

// search asks Apple for episodes and keeps the ones worth a drive.
func (s *podcastSvc) search(ctx context.Context, term string) ([]podcastEp, error) {
	ctx, cancel := context.WithTimeout(ctx, chesterPodcastTimeout)
	defer cancel()
	q := url.Values{"media": {"podcast"}, "entity": {"podcastEpisode"}, "limit": {"25"}, "term": {term}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("error")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errors.New("unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("status " + strconv.Itoa(resp.StatusCode))
	}
	var out struct {
		Results []struct {
			Show    string `json:"collectionName"`
			Episode string `json:"trackName"`
			Date    string `json:"releaseDate"`
			Millis  int64  `json:"trackTimeMillis"`
			URL     string `json:"trackViewUrl"`
			Artist  string `json:"artistName"`
		} `json:"results"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, errors.New("unreadable")
	}
	var eps []podcastEp
	for _, r := range out.Results {
		min := int(r.Millis / 60000)
		if min < chesterPodcastMinMinutes || min > chesterPodcastMaxMinutes || !appleHost(r.URL) || strings.TrimSpace(r.Episode) == "" {
			continue
		}
		year := 0
		if len(r.Date) >= 4 {
			year, _ = strconv.Atoi(r.Date[:4])
		}
		eps = append(eps, podcastEp{Show: r.Show, Episode: r.Episode, Artist: r.Artist, URL: r.URL, Year: year, Minutes: min})
	}
	return eps, nil
}

// chesterPodcastRun is one question's podcast state.
type chesterPodcastRun struct {
	lib      *chesterPodcastLib
	refs     map[string]podcastEp
	byURL    map[string]string
	cards    []chesterPodcastCard
	searches int
	drops    int
	down     bool
}

func newChesterPodcastRun(lib *chesterPodcastLib) *chesterPodcastRun {
	return &chesterPodcastRun{lib: lib, refs: map[string]podcastEp{}, byURL: map[string]string{}}
}

func chesterPodcastTools() []anthropic.BetaToolUnionParam {
	return []anthropic.BetaToolUnionParam{
		clientTool(chesterSearchPodcastsTool,
			"Search podcast episodes by words: a place, its history, its rivers, its people. Search several times with different words. "+
				"It keeps only episodes of 15 to 120 minutes and returns up to "+strconv.Itoa(chesterPodcastResults)+
				" with a reference such as p1, the show, the episode, the year and the minutes. Prefer established publishers (public radio, "+
				"university, museum, park service, well-known history shows) over thin machine-made shows. Recommend only episodes it returns.",
			map[string]any{"query": map[string]any{"type": "string", "description": "Words only, under 80 characters. No coordinates."}},
			[]string{"query"}),
		clientTool(chesterShowPodcastTool,
			"Show the one episode you picked as a card with an Open in Apple Podcasts button. Give its reference from search_podcasts and one short "+
				"sentence on why it fits where the car is. The card goes with the written answer and does not replace it: write the answer after this call.",
			map[string]any{
				"ref": map[string]any{"type": "string", "description": "A reference from search_podcasts, such as p1."},
				"why": map[string]any{"type": "string", "description": "One short sentence saying why it fits where the car is."}},
			[]string{"ref", "why"}),
	}
}

func (p *chesterPodcastRun) search(ctx context.Context, raw string) (string, bool) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil || podcastTerm(in.Query) == "" {
		return "Give a short search in words, such as a place and its history.", true
	}
	if p.searches >= chesterPodcastSearches {
		return "That is enough searching. Pick one episode from the results you have and call show_podcast, or say none fit.", true
	}
	p.searches++
	q := podcastTerm(in.Query)
	eps, err := p.lib.svc.search(ctx, q)
	if err != nil {
		p.lib.melt.Event("chester_podcast_search", map[string]any{"count": 0, "status": 502})
		p.down = true
		return chesterPodcastDown, true
	}
	if len(eps) > chesterPodcastResults {
		eps = eps[:chesterPodcastResults]
	}
	p.lib.melt.Event("chester_podcast_search", map[string]any{"count": len(eps), "status": 200})
	if len(eps) == 0 {
		return "No episode of 15 to 120 minutes matched. Try different words, or say none fit.", false
	}
	var b strings.Builder
	for _, e := range eps {
		ref, seen := p.byURL[e.URL]
		if !seen {
			ref = "p" + strconv.Itoa(len(p.refs)+1)
			p.byURL[e.URL] = ref
			p.refs[ref] = e
		}
		b.WriteString(ref + ": " + cleanLine(e.Show, 100) + " | " + cleanLine(e.Episode, 140))
		if e.Year > 0 {
			b.WriteString(" | " + strconv.Itoa(e.Year))
		}
		b.WriteString(" | " + strconv.Itoa(e.Minutes) + " min\n")
	}
	return b.String(), false
}

func (p *chesterPodcastRun) show(raw string) string {
	var in struct {
		Ref string `json:"ref"`
		Why string `json:"why"`
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		p.drops++
		return "The episode was not readable. Do not call show_podcast again; write the answer."
	}
	ref := strings.TrimSpace(in.Ref)
	if _, err := strconv.Atoi(ref); err == nil {
		ref = "p" + ref
	}
	e, ok := p.refs[ref]
	if !ok {
		p.drops++
		return "No card was shown: that reference was not in this question's search results. Write the answer without recommending an episode."
	}
	if len(p.cards) >= 1 {
		return "One episode per answer: the first is on the page. Write the answer now."
	}
	p.cards = append(p.cards, chesterPodcastCard{Show: cleanLine(e.Show, 120), Episode: cleanLine(e.Episode, 160), Minutes: e.Minutes,
		Year: e.Year, Why: cleanLine(in.Why, 400), Href: e.URL, Label: chesterPodcastLabel})
	return "The episode card is on the page with an Open in Apple Podcasts button. Nothing you wrote before this call was shown to the traveller. " +
		"Now write the whole answer: one short sentence on why it fits, and that tapping the button opens it. Do not call show_podcast again."
}

func chesterPodcastSynth(cs []chesterPodcastCard) string {
	return "Chester picked a podcast episode and wrote nothing about it: " + cs[0].Episode + " from " + cs[0].Show + ". The card below says why."
}
