package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

func seedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// serve feeds the rpc lines through the real loop and returns responses.
func serve(t *testing.T, st *store.Store, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	r := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := Serve(context.Background(), st, config.Default(), r, &out); err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err == nil {
			resps = append(resps, m)
		}
	}
	return resps
}

const initMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

func TestHandshakeAndToolsList(t *testing.T) {
	st := seedStore(t)
	resps := serve(t, st, initMsg,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	init := resps[0]["result"].(map[string]any)
	if init["serverInfo"].(map[string]any)["name"] != "wvf" {
		t.Fatal("bad serverInfo")
	}
	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"scan", "sites", "history", "run", "prune"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}

func contentJSON(t *testing.T, res map[string]any) json.RawMessage {
	t.Helper()
	content := res["result"].(map[string]any)["content"].([]any)
	return json.RawMessage([]byte(content[0].(map[string]any)["text"].(string)))
}

func TestSitesHistoryRunPrune(t *testing.T) {
	st := seedStore(t)
	ctx := context.Background()
	site, err := st.GetOrCreateSite(ctx, "a.test", "http://a.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRun(ctx, &store.Run{SiteID: site.ID, Engine: "cdp",
		Status: "ok", StartedAt: time.Now(), FinishedAt: time.Now(),
		TTFBms: 42}); err != nil {
		t.Fatal(err)
	}
	resps := serve(t, st, initMsg,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sites","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"history","arguments":{"site":"a.test"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"prune","arguments":{}}}`)

	var sites []map[string]any
	if err := json.Unmarshal(contentJSON(t, resps[1]), &sites); err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0]["key"] != "a.test" {
		t.Fatalf("sites wrong: %v", sites)
	}
	var runs []map[string]any
	if err := json.Unmarshal(contentJSON(t, resps[2]), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0]["ttfb_ms"] != 42.0 {
		t.Fatalf("history wrong: %v", runs)
	}
	var pr map[string]any
	if err := json.Unmarshal(contentJSON(t, resps[3]), &pr); err != nil {
		t.Fatal(err)
	}
	if pr["dropped"] != float64(0) {
		t.Fatalf("prune wrong: %v", pr)
	}
}

func TestScanBadURL(t *testing.T) {
	st := seedStore(t)
	resps := serve(t, st, initMsg,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"scan","arguments":{}}}`)
	res := resps[1]["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatal("missing url should be isError")
	}
}

func TestRunToolAndHistoryLimit(t *testing.T) {
	st := seedStore(t)
	ctx := context.Background()
	site, _ := st.GetOrCreateSite(ctx, "b.test", "http://b.test", 10, 7)
	var id int64
	for i := 0; i < 3; i++ {
		id, _ = st.InsertRun(ctx, &store.Run{SiteID: site.ID, Engine: "cdp",
			Status: "ok", StartedAt: time.Now().Add(-time.Duration(3-i) * time.Hour),
			FinishedAt: time.Now(), TTFBms: float64(i)})
	}
	resps := serve(t, st, initMsg,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run","arguments":{"id":`+itoa(id)+`}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"history","arguments":{"site":"b.test","limit":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	var wrapped struct {
		Run map[string]any `json:"run"`
	}
	if err := json.Unmarshal(contentJSON(t, resps[1]), &wrapped); err != nil {
		t.Fatal(err)
	}
	if wrapped.Run["id"] != float64(id) || wrapped.Run["ttfb_ms"] != 2.0 {
		t.Fatalf("run tool wrong: %v", wrapped.Run)
	}
	var runs []map[string]any
	if err := json.Unmarshal(contentJSON(t, resps[2]), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("limit=1 should return 1 run, got %d", len(runs))
	}
	res := resps[3]["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatal("unknown tool should be isError")
	}
}

func itoa(i int64) string {
	return fmt.Sprintf("%d", i)
}

func TestRunToolSurfacesFindings(t *testing.T) {
	st := seedStore(t)
	ctx := context.Background()
	site, _ := st.GetOrCreateSite(ctx, "f.test", "http://f.test", 10, 7)
	id, _ := st.InsertRun(ctx, &store.Run{SiteID: site.ID, Engine: "cdp",
		Status: "ok", StartedAt: time.Now(), FinishedAt: time.Now(),
		DetailJSON: `{"findings":[{"id":"cls","severity":"poor","title":"layout shift 0.307"}]}`})
	resps := serve(t, st, initMsg,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run","arguments":{"id":`+itoa(id)+`}}}`)
	var out struct {
		Run      map[string]any   `json:"run"`
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal(contentJSON(t, resps[1]), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 || out.Findings[0]["id"] != "cls" {
		t.Fatalf("findings not surfaced: %+v", out)
	}
}
