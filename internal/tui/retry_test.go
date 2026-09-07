package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/provider/openrouter"
)

func TestRateLimitCancelAndStaleEvents(t *testing.T) {
	for _, quit := range []bool{false, true} {
		t.Run(fmt.Sprintf("quit=%v", quit), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					fmt.Fprint(w, `{"data":[]}`)
					return
				}
				requests.Add(1)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
			}))
			defer server.Close()
			m := New(Config{PluginMgr: pluginhost.NewManager(false)})
			m.llmProvider = openrouter.New(openrouter.Config{BaseURL: server.URL})
			m.lastCallInputTokens = 123
			m.submitMessage("test")
			defer m.stopModelStream()
			ctx := m.streamContext
			generation := m.streamGeneration
			ch := m.streamCh
			event := m.readNextStreamEvent()()
			updated, _ := m.Update(event)
			m = updated.(Model)
			if !strings.Contains(m.renderInput(), "Rate limited (429)") || !m.streaming {
				t.Fatal("retry status not displayed")
			}
			if m.lastCallInputTokens != 123 || len(m.history) != 1 {
				t.Fatal("retry changed context or history")
			}
			if quit {
				updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			} else {
				updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
				m = updated.(Model)
				updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			}
			m = updated.(Model)
			if ctx.Err() != context.Canceled {
				t.Fatal("UI did not cancel actual provider context")
			}
			select {
			case _, ok := <-ch:
				if ok {
					t.Fatal("unexpected event after cancellation")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("provider backoff not canceled")
			}
			if requests.Load() != 1 {
				t.Fatal("retry issued after cancellation")
			}
			before := len(m.history)
			for _, stale := range []tea.Msg{StreamTextMsg{Text: "stale"}, StreamDoneMsg{}, StreamUsageMsg{InputTokens: 999}} {
				updated, cmd := m.Update(streamEnvelope{Generation: generation, Message: stale})
				m = updated.(Model)
				if cmd != nil || len(m.history) != before || m.lastCallInputTokens != 123 || m.streamBuf.Len() != 0 {
					t.Fatal("queued canceled event changed state")
				}
			}
		})
	}
}
