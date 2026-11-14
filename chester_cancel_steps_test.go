package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// Steps for features/chester_cancel_and_the_daily_cap.feature. The Messages
// API is the apiStub of chester_test.go; nothing here reaches Anthropic.
type cancelWorld struct {
	t       *testing.T
	app     *App
	started chan struct{}
	spend   *Store
	now     time.Time
}

func initChesterCancelScenarios(t *testing.T, sc *godog.ScenarioContext) {
	w := &cancelWorld{t: t}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = cancelWorld{t: t}
		return ctx, nil
	})
	hang := func(in int) {
		w.started = make(chan struct{}, 1)
		h := func(rw http.ResponseWriter, r *http.Request) {
			if in > 0 {
				stream(events(evStart(stubModel, in, 0, 0))...)(rw, r)
			} else {
				rw.Header().Set("Content-Type", "text/event-stream")
				rw.WriteHeader(http.StatusOK)
				if f, ok := rw.(http.Flusher); ok {
					f.Flush()
				}
			}
			select {
			case w.started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
		}
		w.app = chesterApp(t, newAPIStub(t, h))
	}
	sc.Step(`^the stubbed API reports (\d+) input tokens and then waits$`, func(n int) error { hang(n); return nil })
	sc.Step(`^the stubbed API says nothing and waits$`, func() error { hang(0); return nil })
	ask := func(cancel bool, limit time.Duration) {
		if limit > 0 {
			w.app.chester.cfg.Quick.Timeout = limit
		}
		ctx, stop := context.WithCancel(context.Background())
		defer stop()
		if cancel {
			go func() {
				select {
				case <-w.started:
					time.Sleep(100 * time.Millisecond) // let the first event be read
					stop()
				case <-time.After(10 * time.Second):
					stop()
				}
			}()
		}
		req := httptest.NewRequest("POST", "/chester/ask", strings.NewReader(url.Values{"preset": {"howfar"}}.Encode())).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w.app.routes().ServeHTTP(httptest.NewRecorder(), req)
	}
	sc.Step(`^the driver cancels the question while it waits$`, func() error { ask(true, 0); return nil })
	sc.Step(`^the question runs out of time$`, func() error { ask(false, 300*time.Millisecond); return nil })
	row := func() (ChesterQuery, error) {
		rows, err := w.app.store.ChesterQueries(0)
		if err != nil || len(rows) != 1 {
			return ChesterQuery{}, fmt.Errorf("want one logged question, got %d (%v)", len(rows), err)
		}
		return rows[0], nil
	}
	sc.Step(`^the row is charged for the (\d+) input tokens and no written tokens$`, func(n int) error {
		r, err := row()
		if err != nil {
			return err
		}
		want := chesterCost(stubModel, "", chesterUsage{In: int64(n)})
		if r.TokensIn != int64(n) || r.TokensOut != 0 || !near(r.USD, want) {
			return fmt.Errorf("row has %d in, %d out, $%v; want %d in, 0 out, $%v", r.TokensIn, r.TokensOut, r.USD, n, want)
		}
		return nil
	})
	sc.Step(`^the row is charged nothing$`, func() error {
		r, err := row()
		if err != nil {
			return err
		}
		if r.USD != 0 {
			return fmt.Errorf("row is charged $%v, want nothing", r.USD)
		}
		return nil
	})
	sc.Step(`^the row is not marked as an estimate$`, func() error {
		r, err := row()
		if err != nil {
			return err
		}
		if r.USDEstimated {
			return fmt.Errorf("the row is marked as an estimate")
		}
		return nil
	})
	sc.Step(`^the row's note says only what the API reported was charged$`, func() error {
		r, err := row()
		if err != nil {
			return err
		}
		if !strings.Contains(r.Note, "only what the API had reported") {
			return fmt.Errorf("the note reads %q", r.Note)
		}
		return nil
	})
	sc.Step(`^the row is charged more than its input tokens$`, func() error {
		r, err := row()
		if err != nil {
			return err
		}
		if r.USD <= chesterCost(stubModel, "", chesterUsage{In: 70})+1e-9 {
			return fmt.Errorf("a timed-out question was charged only $%v", r.USD)
		}
		return nil
	})

	// the cap's day
	central, _ := time.LoadLocation("America/Chicago")
	sc.Step(`^the day's spending is read at 6:30 PM Central on 13 November$`, func() error {
		w.app = activeTripApp(t)
		w.now = time.Date(2026, 11, 13, 18, 30, 0, 0, central)
		return nil
	})
	put := func(at time.Time, usd float64) error {
		return w.app.store.InsertChesterQuery(&ChesterQuery{User: "push-token", AskedAt: utcString(at), Mode: chesterQuick,
			Question: "earlier", Model: stubModel, USD: usd, PosSource: "none"})
	}
	sc.Step(`^\$2\.00 was spent at 5:50 PM Central that day$`, func() error {
		return put(time.Date(2026, 11, 13, 17, 50, 0, 0, central), 2)
	})
	sc.Step(`^\$1\.00 was spent at 9:00 PM Central the evening before$`, func() error {
		return put(time.Date(2026, 11, 12, 21, 0, 0, 0, central), 1)
	})
	sc.Step(`^the day's spending is \$2\.00$`, func() error {
		got, err := w.app.store.ChesterSpent(w.now)
		if err != nil || !near(got, 2) {
			return fmt.Errorf("the day's spending reads %v (%v), want 2", got, err)
		}
		return nil
	})
}
