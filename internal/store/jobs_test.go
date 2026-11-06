package store

import (
	"context"
	"testing"
)

func TestJobPageRoundTrip(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	site, _ := st.GetOrCreateSite(context.Background(), "p.test", "http://p.test", 0, 0)
	id, err := st.InsertJob(context.Background(), "scan", site.ID, false, "", "maps/North")
	if err != nil {
		t.Fatal(err)
	}
	pg, err := st.JobPage(context.Background(), id)
	if err != nil || pg != "maps/North" {
		t.Fatalf("page: %q %v", pg, err)
	}
}
