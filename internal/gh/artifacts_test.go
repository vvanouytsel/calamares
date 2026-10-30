package gh

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactsAreListed(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"artifacts":[{"id":5,"name":"dist","size_in_bytes":4400000,"expired":false},{"id":6,"name":"old","expired":true}]}`))
	})
	as, err := c.ListArtifacts(context.Background(), "a/b", 7)
	if err != nil || len(as) != 2 || as[0].Name != "dist" || !as[1].Expired {
		t.Errorf("%v %+v", err, as)
	}
}

func TestADownloadFollowsTheRedirectWithoutTheCredentials(t *testing.T) {
	var gotAuth string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(strings.Repeat("z", 100000)))
	}))
	defer storage.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("the first request carries the token")
		}
		http.Redirect(w, r, storage.URL+"/blob", http.StatusFound)
	})
	var out bytes.Buffer
	var last int64
	err := c.DownloadArtifact(context.Background(), "a/b", 5, &out, func(done, total int64) { last = done })
	if err != nil || out.Len() != 100000 || last != 100000 {
		t.Fatalf("%v len=%d progress=%d", err, out.Len(), last)
	}
	// httptest servers share 127.0.0.1 but not the port: Go still treats it as another host.
	if gotAuth != "" {
		t.Errorf("the token reached the storage host: %q", gotAuth)
	}
}
