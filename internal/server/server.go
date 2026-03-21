package server

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	"github.com/denmark/slack-site/internal/urlpath"
	"github.com/denmark/slack-site/models"
	"github.com/denmark/slack-site/search"
	"github.com/uptrace/bun"
)

//go:embed templates/*.html
var templatesFS embed.FS

const (
	conversationPageSize = 50
	searchPageSize       = 20
)

// SearchHit is a single search result for template rendering.
type SearchHit struct {
	ID               string
	ConversationID   string
	ConversationType string
	Ts               string
	Snippet          string
	UserName         string
}

// messageFileView is used when rendering; URLPrivate may be overridden to mirror base + path when MirrorBaseURL is set.
type messageFileView struct {
	URLPrivate string
	Name       string
	Mimetype   string
}

// Server holds the HTTP server dependencies.
type Server struct {
	DB            *bun.DB
	Index         bleve.Index
	tmpl          *template.Template
	MirrorBaseURL string // if set, message file links use this base + computed path from url_private
}

// New creates a Server and parses templates from the given dir, or from embedded FS if templateDir is empty.
// mirrorBaseURL is optional; when set, conversation file links use base URL + path derived from url_private.
func New(db *bun.DB, idx bleve.Index, templateDir string, mirrorBaseURL string) (*Server, error) {
	mirrorBaseURL = strings.TrimSuffix(mirrorBaseURL, "/")
	s := &Server{DB: db, Index: idx, MirrorBaseURL: mirrorBaseURL}
	funcs := template.FuncMap{
		"safeHTML":     func(s string) template.HTML { return template.HTML(s) },
		"formatTs":     formatTs,
		"mimeIsInline": mimeIsInline,
	}
	if templateDir != "" {
		tmpl, err := template.New("").Funcs(funcs).ParseGlob(filepath.Join(templateDir, "*.html"))
		if err != nil {
			return nil, err
		}
		s.tmpl = tmpl
	} else {
		sub, _ := fs.Sub(templatesFS, "templates")
		tmpl, err := template.New("").Funcs(funcs).ParseFS(sub, "*.html")
		if err != nil {
			return nil, err
		}
		s.tmpl = tmpl
	}
	return s, nil
}

// Routes registers HTTP handlers on mux.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /channels", s.handleChannelList)
	mux.HandleFunc("GET /channels/{id}", s.handleConversation("channel"))
	mux.HandleFunc("GET /groups", s.handleGroupList)
	mux.HandleFunc("GET /groups/{id}", s.handleConversation("group"))
	mux.HandleFunc("GET /dms", s.handleDMList)
	mux.HandleFunc("GET /dms/{id}", s.handleConversation("dm"))
	mux.HandleFunc("GET /mpims", s.handleMPIMList)
	mux.HandleFunc("GET /mpims/{id}", s.handleConversation("mpim"))
	mux.HandleFunc("GET /search", s.handleSearch)
}

func (s *Server) executeTemplate(w http.ResponseWriter, name string, data interface{}) {
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderContent executes the named content template with data and returns the result as template.HTML.
func (s *Server) renderContent(name string, data interface{}) (template.HTML, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{"Title": "Home"}
	content, err := s.renderContent("home_content", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Content"] = content
	s.executeTemplate(w, "base", data)
}

func (s *Server) handleChannelList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var channels []models.ChannelRow
	err := s.DB.NewSelect().Model(&channels).Order("name ASC").Scan(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Member counts
	type row struct {
		models.ChannelRow
		MemberCount int
	}
	rows := make([]row, len(channels))
	for i := range channels {
		rows[i] = row{ChannelRow: channels[i]}
		n, _ := s.DB.NewSelect().Model((*models.ChannelMemberRow)(nil)).Where("channel_id = ?", channels[i].ID).Count(ctx)
		rows[i].MemberCount = int(n)
	}
	data := map[string]interface{}{"Title": "Channels", "Channels": rows}
	content, err := s.renderContent("channel_list_content", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Content"] = content
	s.executeTemplate(w, "base", data)
}

func (s *Server) handleGroupList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var groups []models.GroupRow
	err := s.DB.NewSelect().Model(&groups).Order("name ASC").Scan(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		models.GroupRow
		MemberCount int
	}
	rows := make([]row, len(groups))
	for i := range groups {
		rows[i] = row{GroupRow: groups[i]}
		n, _ := s.DB.NewSelect().Model((*models.GroupMemberRow)(nil)).Where("group_id = ?", groups[i].ID).Count(ctx)
		rows[i].MemberCount = int(n)
	}
	data := map[string]interface{}{"Title": "Private channels", "Groups": rows}
	content, err := s.renderContent("group_list_content", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Content"] = content
	s.executeTemplate(w, "base", data)
}

func (s *Server) handleDMList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var dms []models.DMRow
	err := s.DB.NewSelect().Model(&dms).Scan(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		models.DMRow
		MemberCount int
	}
	rows := make([]row, len(dms))
	for i := range dms {
		rows[i] = row{DMRow: dms[i]}
		n, _ := s.DB.NewSelect().Model((*models.DMMemberRow)(nil)).Where("dm_id = ?", dms[i].ID).Count(ctx)
		rows[i].MemberCount = int(n)
	}
	data := map[string]interface{}{"Title": "DMs", "DMs": rows}
	content, err := s.renderContent("dm_list_content", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Content"] = content
	s.executeTemplate(w, "base", data)
}

func (s *Server) handleMPIMList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var mpims []models.MPIMRow
	err := s.DB.NewSelect().Model(&mpims).Order("name ASC").Scan(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		models.MPIMRow
		MemberCount int
	}
	rows := make([]row, len(mpims))
	for i := range mpims {
		rows[i] = row{MPIMRow: mpims[i]}
		n, _ := s.DB.NewSelect().Model((*models.MPIMMemberRow)(nil)).Where("mpim_id = ?", mpims[i].ID).Count(ctx)
		rows[i].MemberCount = int(n)
	}
	data := map[string]interface{}{"Title": "MPIMs", "MPIMs": rows}
	content, err := s.renderContent("mpim_list_content", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Content"] = content
	s.executeTemplate(w, "base", data)
}

func (s *Server) handleConversation(convType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()
		// Resolve conversation name for title (channels/groups/mpims have names; dms use id)
		convName := id
		switch convType {
		case "channel":
			var c models.ChannelRow
			if err := s.DB.NewSelect().Model(&c).Where("id = ?", id).Scan(ctx); err == nil {
				convName = c.Name
			}
		case "group":
			var g models.GroupRow
			if err := s.DB.NewSelect().Model(&g).Where("id = ?", id).Scan(ctx); err == nil {
				convName = g.Name
			}
		case "mpim":
			var m models.MPIMRow
			if err := s.DB.NewSelect().Model(&m).Where("id = ?", id).Scan(ctx); err == nil {
				convName = m.Name
			}
		}

		// Pagination: chronological (ASC). First page = oldest; "after" = cursor for next page (newer messages).
		afterTs := r.URL.Query().Get("after")
		var messages []models.MessageRow
		q := s.DB.NewSelect().Model(&messages).Where("conversation_id = ? AND conversation_type = ?", id, convType).Order("ts ASC").Limit(conversationPageSize + 1)
		if afterTs != "" {
			q = q.Where("ts > ?", afterTs)
		}
		err := q.Scan(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hasMore := len(messages) > conversationPageSize
		if hasMore {
			messages = messages[:conversationPageSize]
		}
		nextAfter := ""
		if len(messages) > 0 {
			nextAfter = messages[len(messages)-1].Ts
		}
		// Load message_files for this page of messages (conversation_id, message_ts)
		messageFiles := make(map[string][]messageFileView)
		if len(messages) > 0 {
			tsList := make([]string, 0, len(messages))
			for _, m := range messages {
				tsList = append(tsList, m.Ts)
			}
			var files []models.MessageFileRow
			err := s.DB.NewSelect().Model(&files).
				Where("message_conversation_id = ? AND message_ts IN (?)", id, bun.In(tsList)).
				Scan(ctx)
			if err == nil {
				for _, f := range files {
					view := messageFileView{URLPrivate: f.URLPrivate, Name: f.Name, Mimetype: f.Mimetype}
					if s.MirrorBaseURL != "" && f.URLPrivate != "" {
						if path, pathErr := urlpath.RelativePath(f.URLPrivate, f.Name); pathErr == nil {
							view.URLPrivate = s.MirrorBaseURL + "/" + path
						}
					}
					messageFiles[f.MessageTs] = append(messageFiles[f.MessageTs], view)
				}
			}
		}
		data := map[string]interface{}{
			"Title":            convName,
			"ConversationID":   id,
			"ConversationType": convType,
			"ConversationName": convName,
			"Messages":         messages,
			"MessageFiles":     messageFiles,
			"HasNewer":         hasMore,
			"NewerAfter":       nextAfter,
			"HasOlder":         afterTs != "",
		}
		content, err := s.renderContent("conversation_content", data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data["Content"] = content
		s.executeTemplate(w, "base", data)
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	page := 0
	if p := r.URL.Query().Get("page"); p != "" {
		if n, _ := parseInt(p); n > 0 {
			page = n - 1
		}
	}
	if s.Index == nil {
		s.executeSearchPage(w, searchData("Search", q, nil, 0, 1, 0, false))
		return
	}
	if q == "" {
		s.executeSearchPage(w, searchData("Search", "", nil, 0, 1, 0, false))
		return
	}
	from := page * searchPageSize
	result, err := search.SearchWithFields(s.Index, q, from, searchPageSize+1, []string{"conversation_id", "ts", "text", "name"})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := len(result.Hits) > searchPageSize
	hits := result.Hits
	if hasMore {
		hits = result.Hits[:searchPageSize]
	}
	// Build search hit view from DocumentMatch (ID = "convID_ts", Fields may be populated)
	searchHits := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		sh := SearchHit{ID: h.ID}
		sh.ConversationID, sh.Ts = splitDocID(h.ID)
		if f := h.Fields; f != nil {
			if v, _ := f["conversation_id"].(string); v != "" {
				sh.ConversationID = v
			}
			if v, _ := f["ts"].(string); v != "" {
				sh.Ts = v
			}
			if v, _ := f["text"].(string); v != "" {
				sh.Snippet = truncateText(v, 200)
			}
			if v, _ := f["name"].(string); v != "" {
				sh.UserName = v
			}
		}
		sh.ConversationType = inferConvType(sh.ConversationID)
		searchHits = append(searchHits, sh)
	}
	nextPage := 0
	if hasMore {
		nextPage = page + 2
	}
	s.executeSearchPage(w, searchData("Search", q, searchHits, result.Total, page+1, nextPage, hasMore))
}

// searchData builds the data map for the search template.
func searchData(title, query string, results []SearchHit, total uint64, page, nextPage int, hasMore bool) map[string]interface{} {
	return map[string]interface{}{
		"Title":    title,
		"Query":    query,
		"Results":  results,
		"Total":    total,
		"Page":     page,
		"NextPage": nextPage,
		"HasMore":  hasMore,
	}
}

func (s *Server) executeSearchPage(w http.ResponseWriter, data map[string]interface{}) {
	content, err := s.renderContent("search_content", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Content"] = content
	s.executeTemplate(w, "base", data)
}

func parseInt(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

// mimeIsInline returns true for mimetypes that should be displayed inline (e.g. images) rather than as download links.
func mimeIsInline(mimetype string) bool {
	return strings.HasPrefix(strings.ToLower(mimetype), "image/")
}

// formatTs formats a Slack timestamp (UTC epoch string, e.g. "1234567890.123456") as "January 2, 2006 at 3:04 PM" in the server's local timezone.
func formatTs(ts interface{}) string {
	var s string
	switch v := ts.(type) {
	case string:
		s = v
	case nil:
		return ""
	default:
		return ""
	}
	if s == "" {
		return ""
	}
	// Slack ts is "seconds.microseconds"; parse the integer part
	secStr := s
	if i := strings.Index(s, "."); i >= 0 {
		secStr = s[:i]
	}
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return s
	}
	t := time.Unix(sec, 0).Local()
	return t.Format("January 2, 2006 at 3:04 PM")
}

// inferConvType returns the conversation type from a Slack conversation ID prefix (C=channel, G=group, D=dm, else mpim).
func inferConvType(convID string) string {
	if convID == "" {
		return "channel"
	}
	switch convID[0] {
	case 'C':
		return "channel"
	case 'G':
		return "group"
	case 'D':
		return "dm"
	default:
		return "mpim"
	}
}

// splitDocID splits Bleve doc ID "conversationID_ts" into conversation ID and ts (ts may contain decimals).
func splitDocID(docID string) (convID, ts string) {
	i := strings.LastIndex(docID, "_")
	if i < 0 {
		return docID, ""
	}
	return docID[:i], docID[i+1:]
}

// voidHTMLTags are HTML void elements; they must not be pushed onto the open-tag stack.
var voidHTMLTags = map[string]struct{}{
	"area": {}, "base": {}, "br": {}, "col": {}, "embed": {}, "hr": {}, "img": {},
	"input": {}, "link": {}, "meta": {}, "param": {}, "source": {}, "track": {}, "wbr": {},
}

func truncateText(s string, max int) string {
	s = strings.TrimSpace(s)
	cut, truncated := truncateCutAtRunes(s, max)
	if !truncated {
		return s
	}
	cut = extendCutPastIncompleteTag(s, cut)
	prefix := s[:cut]
	return prefix + htmlClosingTags(prefix) + "..."
}

// truncateCutAtRunes returns the byte offset after the first max runes; truncated is false if s is shorter.
func truncateCutAtRunes(s string, max int) (cut int, truncated bool) {
	if max <= 0 {
		return 0, s != ""
	}
	runeCount := 0
	for i := 0; i < len(s); {
		if runeCount == max {
			return i, true
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		if w == 0 {
			break
		}
		i += w
		runeCount++
	}
	return len(s), false
}

// extendCutPastIncompleteTag moves the cut forward to just after the next '>' if the cut falls inside a tag (between '<' and '>', respecting quotes).
func extendCutPastIncompleteTag(s string, cut int) int {
	inTag := false
	var quote byte
	for i := 0; i < cut; i++ {
		c := s[i]
		if !inTag {
			if c == '<' {
				inTag = true
				quote = 0
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			inTag = false
		}
	}
	if !inTag {
		return cut
	}
	j := cut
	for j < len(s) {
		c := s[j]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			j++
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			j++
			continue
		}
		if c == '>' {
			return j + 1
		}
		j++
	}
	// No closing '>' — drop the incomplete tag opener so we do not emit broken markup.
	for k := cut - 1; k >= 0; k-- {
		if s[k] == '<' {
			return k
		}
	}
	return cut
}

func skipToTagEnd(s string, start int) int {
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			return i + 1
		}
	}
	return len(s)
}

func parseTagName(s string, i *int) string {
	start := *i
	for *i < len(s) && tagNameByte(s[*i]) {
		*i++
	}
	return strings.ToLower(s[start:*i])
}

func tagNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func tagIsSelfClosingBeforeGT(s string, gt int) bool {
	j := gt - 1
	for j >= 0 && isSpaceByte(s[j]) {
		j--
	}
	return j >= 0 && s[j] == '/'
}

// htmlClosingTags returns closing tags for elements left open at the end of prefix (e.g. after truncation).
func htmlClosingTags(prefix string) string {
	var stack []string
	i := 0
	for i < len(prefix) {
		if prefix[i] != '<' {
			i++
			continue
		}
		start := i
		i++
		if i >= len(prefix) {
			break
		}
		if prefix[i] == '/' {
			i++
			name := parseTagName(prefix, &i)
			i = skipToTagEnd(prefix, i)
			stack = popMatchingTag(stack, name)
			continue
		}
		if prefix[i] == '!' || prefix[i] == '?' {
			i = skipToTagEnd(prefix, i+1)
			continue
		}
		name := parseTagName(prefix, &i)
		if name == "" {
			i++
			continue
		}
		end := skipToTagEnd(prefix, i)
		if end <= start {
			break
		}
		gt := end - 1
		if _, void := voidHTMLTags[name]; void || tagIsSelfClosingBeforeGT(prefix, gt) {
			i = end
			continue
		}
		stack = append(stack, name)
		i = end
	}
	var b strings.Builder
	for j := len(stack) - 1; j >= 0; j-- {
		b.WriteString("</")
		b.WriteString(stack[j])
		b.WriteString(">")
	}
	return b.String()
}

func popMatchingTag(stack []string, name string) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == name {
			return stack[:i]
		}
	}
	return stack
}
