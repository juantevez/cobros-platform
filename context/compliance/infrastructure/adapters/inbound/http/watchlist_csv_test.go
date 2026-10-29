package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/compliance/application"
	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

func TestParseWatchlistCSV(t *testing.T) {
	t.Run("columns in any order, BOM, optional country, line numbers", func(t *testing.T) {
		in := "\ufefflist_type, full_name ,external_ref\nsanctions,Vladimir Petrov,SDN-36\npep,\"Pérez, José\",X-2\n"
		rows, err := parseWatchlistCSV(strings.NewReader(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %+v", rows)
		}
		want := application.WatchlistFeedRow{Line: 3, ExternalRef: "X-2", FullName: "Pérez, José", ListType: "pep"}
		if rows[1] != want || rows[0].Line != 2 || rows[0].ExternalRef != "SDN-36" {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("format errors", func(t *testing.T) {
		cases := map[string]string{
			"empty":          "",
			"missing column": "external_ref,full_name\n1,x\n",
			"ragged row":     "external_ref,full_name,list_type\n1,x\n",
		}
		for name, in := range cases {
			if _, err := parseWatchlistCSV(strings.NewReader(in)); !errors.Is(err, errFeedFormat) {
				t.Errorf("%s: expected errFeedFormat, got %v", name, err)
			}
		}
	})
}

func TestImportWatchlist(t *testing.T) {
	post := func(h *ComplianceHandler, query, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/compliance/watchlist/import?"+query, bytes.NewBufferString(body))
		h.ImportWatchlist(c)
		return rec
	}
	feed := "external_ref,full_name,list_type,country\nSDN-1,Vladimir Petrov,sanctions,RU\nSDN-2,Ivan Sokolov,sanctions,RU\n"

	t.Run("applies and returns the summary", func(t *testing.T) {
		w := &fakeWatchlist{}
		rec := post(newHandler(newAlertRepo(), w), "source=ofac", feed)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var res application.ImportWatchlistResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res.Source != "OFAC" || res.Added != 2 || res.DryRun {
			t.Errorf("result = %+v", res)
		}
		if len(w.added) != 2 {
			t.Errorf("added = %d", len(w.added))
		}
	})

	t.Run("dry run does not write", func(t *testing.T) {
		w := &fakeWatchlist{}
		rec := post(newHandler(newAlertRepo(), w), "source=OFAC&dry_run=true", feed)
		if rec.Code != http.StatusOK || len(w.added) != 0 {
			t.Fatalf("status = %d, added = %d", rec.Code, len(w.added))
		}
	})

	t.Run("invalid rows yield 422 with problems", func(t *testing.T) {
		rec := post(newHandler(newAlertRepo(), &fakeWatchlist{}), "source=OFAC",
			"external_ref,full_name,list_type\n1,x,terror\n")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d", rec.Code)
		}
		var body struct {
			Error    string   `json:"error"`
			Problems []string `json:"problems"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error != domain.ErrInvalidWatchlistFeed.Error() || len(body.Problems) != 1 || !strings.Contains(body.Problems[0], "line 2") {
			t.Errorf("body = %+v", body)
		}
	})

	t.Run("bad csv yields 400", func(t *testing.T) {
		if rec := post(newHandler(newAlertRepo(), &fakeWatchlist{}), "source=OFAC", "foo,bar\n"); rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		w := &fakeWatchlist{addErr: errors.New("pq: secret")}
		rec := post(newHandler(newAlertRepo(), w), "source=OFAC", feed)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})
}
