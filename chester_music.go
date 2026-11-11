package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Chester and the music library. Chester can search the MusicOrgan library
// (through the same roadtracksSvc the /driver search box uses) and recommend an
// album as a card with a Play link. Kris, 2026-11-08: no mini-player and no
// audio through RoadTrip; Play opens the album page on the library in a new
// tab, and music does not start by itself.
//
// The model never sees a URL or the key. search_music answers with each
// album's title, artist, year and a short reference (m1, m2, ...) that is good
// for this one question only. show_music takes references back, and the server
// builds each card from the address its own search returned. A reference this
// question's searches did not return is dropped, so the model cannot invent an
// album or a link.

const (
	chesterSearchMusicTool = "search_music"
	chesterShowMusicTool   = "show_music"
	chesterMusicResults    = 8 // albums one search hands the model
	chesterMusicCards      = 3 // cards one answer may show

	chesterMusicDown = "The music library is unavailable right now. Tell the traveller so plainly, and do not suggest an album from memory."
)

// chesterMusicPrompt is appended to the system prompt only when the library is
// on, so with RoadTracks off the prompt is the one it always was.
const chesterMusicPrompt = `

Music. When the traveller asks for music, call search_music with an artist or an album title. The library matches album and artist names only, never moods or genres, so for a mood think of artists that suit it and search several of them at once, as separate search_music calls in the same turn. Recommend only from the albums the searches return. Then call show_music with the references of the albums you will recommend (one to three) and two or three plain sentences on why you chose each: what about this album fits this stretch of road, the hour and what the traveller asked. The page turns each into a card with a Play button that opens the album in the music library; you cannot play anything yourself and the music does not start by itself, so say that they tap Play to open it. If search_music says the library is unavailable, say so plainly and do not suggest an album. Write the answer after your last tool call, as for places.`

// chesterMusicLib is the library as one question sees it; nil is the feature off.
type chesterMusicLib struct {
	svc  *roadtracksSvc
	melt *meltSink
	// one caps the cards at a single album: Drive Tracks asks for exactly one.
	one bool
}

// chesterMusicCard is an album as the page gets it. Href is built here from
// the library's own answer; Label is always "Play".
type chesterMusicCard struct {
	// AlbumID is the library's own number for the album. It is what the card
	// player is started with; it carries no address and no key.
	AlbumID int64  `json:"album_id,omitempty"`
	Album   string `json:"album"`
	Artist  string `json:"artist"`
	Year    int    `json:"year,omitempty"`
	Why     string `json:"why"`
	Href    string `json:"href"`
	Label   string `json:"label"`
}

// chesterMusicRun is one question's music state. Nothing outlives the question.
type chesterMusicRun struct {
	lib   *chesterMusicLib
	refs  map[string]trackAlbum
	byURL map[string]string
	cards []chesterMusicCard
	shown map[string]bool
	drops int
	// down is set when a search could not reach the library.
	down bool
}

func newChesterMusicRun(lib *chesterMusicLib) *chesterMusicRun {
	return &chesterMusicRun{lib: lib, refs: map[string]trackAlbum{}, byURL: map[string]string{}, shown: map[string]bool{}}
}

func (m *chesterMusicRun) limit() int {
	if m.lib.one {
		return 1
	}
	return chesterMusicCards
}

// chesterMusicTools are the two tools offered when the library is on.
func chesterMusicTools() []anthropic.BetaToolUnionParam {
	return []anthropic.BetaToolUnionParam{
		clientTool(chesterSearchMusicTool,
			"Search the music library for albums by artist or album title. It matches names only, so a mood or genre finds nothing. "+
				"Several searches may go in one turn. "+
				"It returns up to "+strconv.Itoa(chesterMusicResults)+" albums, each with a reference such as m1. "+
				"Recommend only albums it returns.",
			map[string]any{"query": map[string]any{"type": "string", "description": "An artist or an album title, under 80 characters."}},
			[]string{"query"}),
		clientTool(chesterShowMusicTool,
			"Show the albums you recommend as cards with a Play button. Give one to "+strconv.Itoa(chesterMusicCards)+
				" references from search_music and one short sentence on why each suits the drive. "+
				"The cards go with the written answer and do not replace it: write the answer after this call.",
			map[string]any{"albums": map[string]any{"type": "array", "maxItems": chesterMusicCards,
				"items": map[string]any{"type": "object",
					"properties": map[string]any{
						"ref": map[string]any{"type": "string", "description": "A reference from search_music, such as m1."},
						"why": map[string]any{"type": "string", "description": "Two or three sentences saying why you chose it."}},
					"required": []string{"ref", "why"}, "additionalProperties": false}}},
			[]string{"albums"}),
	}
}

// clientTool is a tool the server runs itself: an object input of the given
// properties, all required as listed.
func clientTool(name, desc string, props map[string]any, required []string) anthropic.BetaToolUnionParam {
	return anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
		Name: name, Description: anthropic.String(desc),
		InputSchema: anthropic.BetaToolInputSchemaParam{Properties: props, Required: required,
			ExtraFields: map[string]any{"additionalProperties": false}},
	}}
}

// search runs search_music. The answer is what the model is told: never an
// address, never the key, and a failure is a plain sentence.
func (m *chesterMusicRun) search(ctx context.Context, raw string) (string, bool) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil || cleanLine(in.Query, 80) == "" {
		return "Give a short search, such as an artist or an album.", true
	}
	q := cleanLine(in.Query, 80)
	albums, err := m.lib.svc.search(ctx, q)
	if err != nil {
		m.lib.melt.Event("chester_music_search", map[string]any{"qlen": len(q), "count": 0, "status": 502})
		m.down = true
		return chesterMusicDown, true
	}
	// An album the library says it cannot play is never offered: it is dropped
	// before any reference is numbered, so the model cannot pick it.
	playable := albums[:0:0]
	for _, a := range albums {
		if a.Playable {
			playable = append(playable, a)
		}
	}
	albums = playable
	if len(albums) > chesterMusicResults {
		albums = albums[:chesterMusicResults]
	}
	m.lib.melt.Event("chester_music_search", map[string]any{"qlen": len(q), "count": len(albums), "status": 200})
	if len(albums) == 0 {
		return "No playable album in the library matched. Say so, or try one different search.", false
	}
	var b strings.Builder
	for _, a := range albums {
		ref, seen := m.byURL[a.URL]
		if !seen {
			ref = "m" + strconv.Itoa(len(m.refs)+1)
			m.byURL[a.URL] = ref
			m.refs[ref] = a
		}
		b.WriteString(ref + ": " + cleanLine(a.Album, 120) + " by " + cleanLine(a.Albumartist, 120))
		if a.Year > 0 {
			b.WriteString(" (" + strconv.Itoa(a.Year) + ")")
		}
		b.WriteString("\n")
	}
	return b.String(), false
}

// show runs show_music: each reference becomes a card if, and only if, this
// question's own searches returned it.
func (m *chesterMusicRun) show(raw string) string {
	var in struct {
		Albums []struct {
			Ref string `json:"ref"`
			Why string `json:"why"`
		} `json:"albums"`
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		m.drops++
		return "The album list was not readable. Do not call show_music again; write the answer."
	}
	for _, it := range in.Albums {
		a, ok := m.refs[strings.TrimSpace(it.Ref)]
		if !ok || m.shown[a.URL] || len(m.cards) >= m.limit() {
			if !ok {
				m.drops++
			}
			continue
		}
		m.shown[a.URL] = true
		m.cards = append(m.cards, chesterMusicCard{AlbumID: a.ID, Album: cleanLine(a.Album, 120), Artist: cleanLine(a.Albumartist, 120),
			Year: a.Year, Why: cleanLine(it.Why, 700), Href: a.URL, Label: "Play"})
	}
	if len(m.cards) == 0 {
		return "No card was shown: those references were not in this question's search results. Write the answer without recommending an album."
	}
	return "The album cards are on the page with a Play button. Nothing you wrote before this call was shown to the traveller. " +
		"Now write the whole answer: a few plain sentences saying why, and that tapping Play opens the album. Do not call show_music again."
}

// synth is the sentence the page shows when the model picked albums and wrote
// no words, built here from the cards (as chesterSynthAnswer is for places).
func chesterMusicSynth(cs []chesterMusicCard) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Album+" by "+c.Artist)
	}
	if len(names) == 1 {
		return "Chester picked an album and wrote nothing about it: " + names[0] + ". The card below says why."
	}
	return "Chester picked " + strconv.Itoa(len(names)) + " albums and wrote nothing about them: " +
		strings.Join(names, "; ") + ". The cards below say why."
}
