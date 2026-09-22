package telegram

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAPI is a scripted Bot API: every request is captured and each method
// pops its next scripted response, defaulting to ok with an empty result.
type fakeAPI struct {
	t *testing.T

	server *httptest.Server

	mu    sync.Mutex
	calls []string // "<method>\n<json body>"
	// script queues raw JSON responses per method; exhaustion falls back to
	// `{"ok":true,"result":{}}` (or `[]` for getUpdates).
	script map[string][]string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, script: map[string][]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", f.serve)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/bot")
	if i := strings.LastIndex(method, "/"); i >= 0 {
		method = method[i+1:]
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, method+"\n"+string(body))
	var resp string
	if q := f.script[method]; len(q) > 0 {
		resp = q[0]
		f.script[method] = q[1:]
	} else if method == "getUpdates" {
		resp = `{"ok":true,"result":[]}`
	} else {
		resp = `{"ok":true,"result":{}}`
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(resp))
}

// respond queues responses for a method, in order.
func (f *fakeAPI) respond(method string, bodies ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[method] = append(f.script[method], bodies...)
}

// callsOf returns the decoded request bodies sent to a method.
func (f *fakeAPI) callsOf(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		name, body, _ := strings.Cut(c, "\n")
		if name != method {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			f.t.Fatalf("captured body for %s is not JSON: %v", method, err)
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeAPI) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if name, _, _ := strings.Cut(c, "\n"); name == method {
			n++
		}
	}
	return n
}
