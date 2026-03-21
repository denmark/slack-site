package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/denmark/slack-site/db"
	"github.com/denmark/slack-site/internal/urlpath"
	"github.com/denmark/slack-site/models"
	"github.com/denmark/slack-site/search"
)

func testServer(t *testing.T, mirrorBase string) *Server {
	t.Helper()
	p := filepath.Join(t.TempDir(), "slack.db")
	database, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	srv, err := New(database, nil, "", mirrorBase)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// testServerWithBleve creates a temp DB and a fresh Bleve index under the same directory, wired into the server.
func testServerWithBleve(t *testing.T, mirrorBase string) *Server {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "slack.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := search.NewIndex(tmp)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	srv, err := New(database, idx, "", mirrorBase)
	if err != nil {
		_ = search.Close(idx)
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = search.Close(idx)
		_ = database.Close()
	})
	return srv
}

func TestNew_embeddedTemplates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slack.db")
	database, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	srv, err := New(database, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if srv.tmpl == nil {
		t.Fatal("expected parsed templates")
	}
}

func TestNew_trimsTrailingSlashOnMirrorBase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slack.db")
	database, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	srv, err := New(database, nil, "", "https://cdn.example/mirror/")
	if err != nil {
		t.Fatal(err)
	}
	if srv.MirrorBaseURL != "https://cdn.example/mirror" {
		t.Errorf("MirrorBaseURL = %q; want trailing slash stripped", srv.MirrorBaseURL)
	}
}

func TestNew_invalidTemplateDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slack.db")
	database, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	emptyDir := filepath.Join(t.TempDir(), "notemplates")
	_, err = New(database, nil, emptyDir, "")
	if err == nil {
		t.Fatal("expected error when no *.html in templateDir")
	}
}

func TestFormatTs(t *testing.T) {
	sec := int64(1234567890)
	ts := strconv.FormatInt(sec, 10) + ".123456"
	want := time.Unix(sec, 0).Local().Format("January 2, 2006 at 3:04 PM")
	if got := formatTs(ts); got != want {
		t.Errorf("formatTs(%q) = %q; want %q", ts, got, want)
	}
	if formatTs("") != "" {
		t.Error("empty string")
	}
	if formatTs(nil) != "" {
		t.Error("nil")
	}
	if got := formatTs("not-a-number"); got != "not-a-number" {
		t.Errorf("invalid ts returns original: got %q", got)
	}
}

func TestMimeIsInline(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"image/png", true},
		{"Image/JPEG", true},
		{"text/plain", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := mimeIsInline(tt.mime); got != tt.want {
			t.Errorf("mimeIsInline(%q) = %v; want %v", tt.mime, got, tt.want)
		}
	}
}

func TestInferConvType(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"", "channel"},
		{"C123", "channel"},
		{"G456", "group"},
		{"D789", "dm"},
		{"W012", "mpim"},
	}
	for _, tt := range tests {
		if got := inferConvType(tt.id); got != tt.want {
			t.Errorf("inferConvType(%q) = %q; want %q", tt.id, got, tt.want)
		}
	}
}

func TestSplitDocID(t *testing.T) {
	tests := []struct {
		docID  string
		wantC  string
		wantTs string
	}{
		{"C1_1234.56", "C1", "1234.56"},
		{"Csolo", "Csolo", ""},
		{"a_b_c", "a_b", "c"},
	}
	for _, tt := range tests {
		c, ts := splitDocID(tt.docID)
		if c != tt.wantC || ts != tt.wantTs {
			t.Errorf("splitDocID(%q) = (%q,%q); want (%q,%q)", tt.docID, c, ts, tt.wantC, tt.wantTs)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantOK  bool
	}{
		{"1", 1, true},
		{"0", 0, false},
		{"-3", -3, false},
		{"x", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		n, ok := parseInt(tt.in)
		if n != tt.want || ok != tt.wantOK {
			t.Errorf("parseInt(%q) = (%d,%v); want (%d,%v)", tt.in, n, ok, tt.want, tt.wantOK)
		}
	}
}

func TestSearchData(t *testing.T) {
	hits := []SearchHit{{ID: "x"}}
	m := searchData("Title", "q", hits, 42, 2, 3, true)
	if m["Title"] != "Title" || m["Query"] != "q" || m["Total"] != uint64(42) {
		t.Fatalf("unexpected map: %#v", m)
	}
	if m["Page"] != 2 || m["NextPage"] != 3 || m["HasMore"] != true {
		t.Fatalf("pagination fields: %#v", m)
	}
	res, _ := m["Results"].([]SearchHit)
	if len(res) != 1 || res[0].ID != "x" {
		t.Fatalf("Results: %#v", m["Results"])
	}
}

func TestHandleHome(t *testing.T) {
	srv := testServer(t, "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Home") {
		t.Errorf("body should mention Home: %q", body)
	}
}

func TestHandleChannelList_empty(t *testing.T) {
	srv := testServer(t, "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/channels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandleConversation_emptyIDNotFound(t *testing.T) {
	srv := testServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/channels/ignored", nil)
	req.SetPathValue("id", "")
	srv.handleConversation("channel")(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("empty id: got status %d", rec.Code)
	}
}

func TestHandleConversation_rendersMessageAndMirrorLink(t *testing.T) {
	srv := testServer(t, "https://mirror.example.com")
	database := srv.DB
	ctx := context.Background()

	_, err := database.NewInsert().Model(&models.ChannelRow{
		ID:   "Ctestconv",
		Name: "test-channel",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ts := "1111.222222"
	_, err = database.NewInsert().Model(&models.MessageRow{
		ConversationID:   "Ctestconv",
		ConversationType: "channel",
		UserID:           "U1",
		Type:             "message",
		Ts:               ts,
		Text:             "visible body",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	privURL := "https://files.slack.com/files-pri/T1-F1/photo.png"
	_, err = database.NewInsert().Model(&models.MessageFileRow{
		MessageConversationID: "Ctestconv",
		MessageTs:             ts,
		SlackFileID:           "Fslack1",
		URLPrivate:            privURL,
		Name:                  "photo.png",
		Mimetype:              "image/png",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/channels/Ctestconv", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "visible body") {
		t.Errorf("expected message text in body")
	}
	rel, err := urlpath.RelativePath(privURL, "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	wantMirror := "https://mirror.example.com/" + rel
	if !strings.Contains(body, wantMirror) {
		t.Errorf("body should contain mirror URL %q\nbody: %s", wantMirror, body)
	}
}

func TestHandleSearch_nilIndex(t *testing.T) {
	srv := testServer(t, "")
	srv.Index = nil
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?q=hello", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandleSearch_emptyQuery(t *testing.T) {
	srv := testServer(t, "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandleGroupList_empty(t *testing.T) {
	srv := testServer(t, "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/groups", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandleDMList_empty(t *testing.T) {
	srv := testServer(t, "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dms", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandleMPIMList_empty(t *testing.T) {
	srv := testServer(t, "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mpims", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandleSearch_bleveSingleHit(t *testing.T) {
	srv := testServerWithBleve(t, "")
	docs := []*models.SearchDocument{{
		ID:              "Csearch1_1500000.000001",
		ConversationID:  "Csearch1",
		UserID:          "U1",
		Ts:              "1500000.000001",
		Text:            "uniqueAlphaSearchToken banana",
		UserProfileName: "Pat Searcher",
		Team:            "T1",
	}}
	if err := search.BatchIndexMessages(srv.Index, docs); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	q := "/search?q=" + url.QueryEscape("uniqueAlphaSearchToken")
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, q, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Csearch1") {
		t.Errorf("expected conversation id in body")
	}
	if !strings.Contains(body, "Pat Searcher") {
		t.Errorf("expected indexed name in body")
	}
	if !strings.Contains(body, "1 result(s)") {
		t.Errorf("expected total count in body: %s", body)
	}
	if !strings.Contains(body, "uniqueAlphaSearchToken") && !strings.Contains(body, "banana") {
		t.Errorf("expected snippet from indexed text")
	}
}

func TestHandleSearch_blevePagination(t *testing.T) {
	srv := testServerWithBleve(t, "")
	const token = "commonPagetestTokenZZ"
	docs := make([]*models.SearchDocument, 22)
	for i := range docs {
		ts := fmt.Sprintf("%d.000000", 1600000+i)
		docs[i] = &models.SearchDocument{
			ID:              "Cpagi_" + ts,
			ConversationID:  "Cpagi",
			UserID:          "U1",
			Ts:              ts,
			Text:            token + " rowidx-" + strconv.Itoa(i),
			UserProfileName: "User" + strconv.Itoa(i),
		}
	}
	if err := search.BatchIndexMessages(srv.Index, docs); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.Routes(mux)

	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape(token), nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("page1 status %d", rec1.Code)
	}
	b1 := rec1.Body.String()
	if !strings.Contains(b1, "22 result(s)") {
		t.Errorf("page1: want 22 results in body")
	}
	if strings.Count(b1, "View conversation") != searchPageSize {
		t.Errorf("page1: want %d hit rows, got %d", searchPageSize, strings.Count(b1, "View conversation"))
	}
	if !strings.Contains(b1, "page=2") || !strings.Contains(b1, "Next page") {
		t.Errorf("page1: missing next page link")
	}

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape(token)+"&page=2", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("page2 status %d", rec2.Code)
	}
	b2 := rec2.Body.String()
	if strings.Count(b2, "View conversation") != 2 {
		t.Errorf("page2: want 2 hit rows, got %d", strings.Count(b2, "View conversation"))
	}
}

func TestHandleConversation_channelTitleFromRow(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()
	_, err := srv.DB.NewInsert().Model(&models.ChannelRow{
		ID:   "CtitleCh",
		Name: "resolved-channel-name",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.DB.NewInsert().Model(&models.MessageRow{
		ConversationID:   "CtitleCh",
		ConversationType: "channel",
		UserID:           "U1",
		Type:             "message",
		Ts:               "1700000.000001",
		Text:             "hi",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/channels/CtitleCh", nil))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "resolved-channel-name") {
		t.Errorf("expected channel name in page title area")
	}
}

func TestHandleConversation_groupMpimDmTitles(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	_, err := srv.DB.NewInsert().Model(&models.GroupRow{
		ID:   "GtitleG",
		Name: "resolved-group-name",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.DB.NewInsert().Model(&models.MPIMRow{
		ID:   "MtitleM",
		Name: "resolved-mpim-name",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.DB.NewInsert().Model(&models.DMRow{
		ID:      "DtitleD",
		Created: 1,
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.Routes(mux)

	for _, tc := range []struct {
		path, convID, convType, ts, wantInBody string
	}{
		{"/groups/GtitleG", "GtitleG", "group", "1800001.000001", "resolved-group-name"},
		{"/mpims/MtitleM", "MtitleM", "mpim", "1800002.000001", "resolved-mpim-name"},
		{"/dms/DtitleD", "DtitleD", "dm", "1800003.000001", "DtitleD"},
	} {
		_, err = srv.DB.NewInsert().Model(&models.MessageRow{
			ConversationID:   tc.convID,
			ConversationType: tc.convType,
			UserID:           "U1",
			Type:             "message",
			Ts:               tc.ts,
			Text:             "hello",
		}).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tc.wantInBody) {
			t.Errorf("%s: body should contain %q", tc.path, tc.wantInBody)
		}
	}
}

func TestHandleConversation_paginationHasNewerAndOlder(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()
	_, err := srv.DB.NewInsert().Model(&models.ChannelRow{
		ID:   "Cpage51",
		Name: "page-test",
	}).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]models.MessageRow, 51)
	for i := range rows {
		rows[i] = models.MessageRow{
			ConversationID:   "Cpage51",
			ConversationType: "channel",
			UserID:           "U1",
			Type:             "message",
			Ts:               fmt.Sprintf("%d.000000", 3000000+i),
			Text:             fmt.Sprintf("page-body-%02d", i),
		}
	}
	_, err = srv.DB.NewInsert().Model(&rows).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv.Routes(mux)

	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/channels/Cpage51", nil))
	if rec1.Code != http.StatusOK {
		t.Fatal(rec1.Code)
	}
	b1 := rec1.Body.String()
	if !strings.Contains(b1, "page-body-00") || !strings.Contains(b1, "page-body-49") {
		t.Errorf("first page should include oldest 50 messages")
	}
	if strings.Contains(b1, "page-body-50") {
		t.Errorf("first page should not include 51st message")
	}
	if !strings.Contains(b1, "Next (newer) messages") || !strings.Contains(b1, "after=3000049.000000") {
		t.Errorf("first page should link to next with correct cursor")
	}

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/channels/Cpage51?after=3000049.000000", nil))
	if rec2.Code != http.StatusOK {
		t.Fatal(rec2.Code)
	}
	b2 := rec2.Body.String()
	if !strings.Contains(b2, "page-body-50") {
		t.Errorf("second page should include newest message")
	}
	if strings.Contains(b2, "page-body-00") {
		t.Errorf("second page should not include oldest message")
	}
	if !strings.Contains(b2, "First page") {
		t.Errorf("second page should offer link back to first (HasOlder)")
	}
	if strings.Contains(b2, "Next (newer) messages") {
		t.Errorf("second page should not offer another newer page")
	}
}
