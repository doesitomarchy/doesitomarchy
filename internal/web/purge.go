package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// purger asks Cloudflare to drop its cached pages after results change.
// Every page's footer shows coverage, so it purges everything; moderation
// is rare, and the debounce turns a session of accepts into one purge.
type purger struct {
	zone, token string
	log         *slog.Logger
	delay       time.Duration
	endpoint    string // overridden in tests

	mu    sync.Mutex
	timer *time.Timer
	done  chan struct{} // closed after each purge attempt (tests)
}

func newPurger(zone, token string, log *slog.Logger) *purger {
	return &purger{zone: zone, token: token, log: log, delay: 10 * time.Second,
		endpoint: "https://api.cloudflare.com/client/v4/zones/%s/purge_cache"}
}

// schedule purges after the debounce delay; calls within it are merged.
func (p *purger) schedule() {
	if p == nil || p.zone == "" || p.token == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(p.delay, p.run)
}

func (p *purger) run() {
	defer func() {
		p.mu.Lock()
		if p.done != nil {
			close(p.done)
			p.done = nil
		}
		p.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(p.endpoint, p.zone), strings.NewReader(`{"purge_everything":true}`))
	if err != nil {
		p.log.Error("cache purge", "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.log.Error("cache purge", "err", err)
		return
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"success":true`) {
		p.log.Error("cache purge", "status", res.StatusCode, "body", strings.TrimSpace(string(body)))
		return
	}
	p.log.Info("cache purged")
}
