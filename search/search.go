package search

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
	"github.com/denmark/slack-site/models"
)

// searchAnalyzer matches slackIndexMapping text fields: unicode tokenizer, lowercase,
// English stopwords — same as Bleve's "standard" analyzer (no Porter stemming).
// Stemming would merge "process" and "processing", so a search for one would match the other.
const searchAnalyzer = "standard"

const (
	// IndexDir is the Bleve index directory name under a data directory (e.g. --output / --data).
	IndexDir = "slack.bleve"
	// MessageIndexBatchSize is the number of message documents to send to Bleve per batch (recommended 100-1000).
	// Used by ingest and reindex for consistent index batch sizing.
	MessageIndexBatchSize = 500
)

// IndexPath returns the Bleve index path under outputDir.
func IndexPath(outputDir string) string {
	return filepath.Join(outputDir, IndexDir)
}

// NewIndex creates a new Bleve index at outputDir/slack.bleve. Removes existing index at that path.
func NewIndex(outputDir string) (bleve.Index, error) {
	path := IndexPath(outputDir)
	_ = os.RemoveAll(path)
	mapping := slackIndexMapping()
	return bleve.New(path, mapping)
}

// OpenExisting opens an existing Bleve index at the given path (e.g. outputDir/slack.bleve from ingest).
func OpenExisting(path string) (bleve.Index, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("index not found: %s (run ingest first)", path)
		}
		return nil, fmt.Errorf("stat index: %w", err)
	}
	return bleve.Open(path)
}

// slackIndexMapping builds an index mapping that includes the "name" field of user_profile (indexed and searchable).
func slackIndexMapping() *mapping.IndexMappingImpl {
	idxMapping := bleve.NewIndexMapping()
	docMapping := bleve.NewDocumentMapping()

	// Text fields (use "standard", not "en", so e.g. "processing" and "process" stay distinct tokens).
	textField := bleve.NewTextFieldMapping()
	textField.Analyzer = searchAnalyzer
	docMapping.AddFieldMappingsAt("id", textField)
	docMapping.AddFieldMappingsAt("conversation_id", textField)
	docMapping.AddFieldMappingsAt("user_id", textField)
	docMapping.AddFieldMappingsAt("ts", textField)
	docMapping.AddFieldMappingsAt("text", textField)
	docMapping.AddFieldMappingsAt("team", textField)
	// "name" field of user_profile (mapping as specified in plan)
	docMapping.AddFieldMappingsAt("name", textField)

	// Align default search-time analysis with indexed fields (QueryStringQuery uses _all → default analyzer).
	idxMapping.DefaultAnalyzer = searchAnalyzer

	idxMapping.DefaultMapping = docMapping
	return idxMapping
}

func applySearchAnalyzerToQuery(q blevequery.Query) {
	if q == nil {
		return
	}
	switch t := q.(type) {
	case *blevequery.MatchQuery:
		if t.Analyzer == "" {
			t.Analyzer = searchAnalyzer
		}
	case *blevequery.MatchPhraseQuery:
		if t.Analyzer == "" {
			t.Analyzer = searchAnalyzer
		}
	case *blevequery.BooleanQuery:
		applySearchAnalyzerToQuery(t.Must)
		applySearchAnalyzerToQuery(t.Should)
		applySearchAnalyzerToQuery(t.MustNot)
	case *blevequery.ConjunctionQuery:
		for _, c := range t.Conjuncts {
			applySearchAnalyzerToQuery(c)
		}
	case *blevequery.DisjunctionQuery:
		for _, c := range t.Disjuncts {
			applySearchAnalyzerToQuery(c)
		}
	}
}

// SearchDocumentForMessage returns a search document for a message (for batch or single index).
// text is the message body to index (e.g. HTML-rendered from rich_text blocks or plain msg.Text).
func SearchDocumentForMessage(conversationID, ts string, msg *models.Message, text string) *models.SearchDocument {
	name := ""
	if msg.UserProfile != nil {
		name = msg.UserProfile.Name
	}
	return &models.SearchDocument{
		ID:              conversationID + "_" + ts,
		ConversationID:  conversationID,
		UserID:          msg.User,
		Ts:              msg.Ts,
		Text:            text,
		UserProfileName: name,
		Team:            msg.Team,
	}
}

// SearchDocumentForMessageRow returns a search document from a database MessageRow (for reindexing from DB).
func SearchDocumentForMessageRow(row *models.MessageRow) *models.SearchDocument {
	return &models.SearchDocument{
		ID:              row.ConversationID + "_" + row.Ts,
		ConversationID:  row.ConversationID,
		UserID:          row.UserID,
		Ts:              row.Ts,
		Text:            row.Text,
		UserProfileName: row.UserProfileName,
		Team:            row.Team,
	}
}

// BatchIndexMessages indexes multiple message documents in one batch (much faster than IndexMessage per doc).
func BatchIndexMessages(idx bleve.Index, docs []*models.SearchDocument) error {
	if len(docs) == 0 {
		return nil
	}
	batch := idx.NewBatch()
	for _, doc := range docs {
		_ = batch.Index(doc.ID, doc)
	}
	return idx.Batch(batch)
}

// IndexUser indexes a user for search (optional; plan says "index of all of the data" and "name field of user_profile" - we index messages with user_profile.name; can also index users by name).
func IndexUser(idx bleve.Index, u *models.User) error {
	doc := map[string]interface{}{
		"id":           u.ID,
		"name":         u.Name,
		"real_name":    u.Profile.RealName,
		"display_name": u.Profile.DisplayName,
		"email":        u.Profile.Email,
	}
	return idx.Index("user_"+u.ID, doc)
}

// Close closes the index.
func Close(idx bleve.Index) error {
	if idx == nil {
		return nil
	}
	return idx.Close()
}

// Search runs a query string against the index (helper).
func Search(idx bleve.Index, q string, from, size int) (*bleve.SearchResult, error) {
	return SearchWithFields(idx, q, from, size, nil)
}

// SearchWithFields runs a query and returns hits with the given fields populated (e.g. conversation_id, ts, text).
func SearchWithFields(idx bleve.Index, q string, from, size int, fields []string) (*bleve.SearchResult, error) {
	if idx == nil {
		return nil, fmt.Errorf("index is nil")
	}
	qs := bleve.NewQueryStringQuery(q)
	parsed, err := qs.Parse()
	if err != nil {
		return nil, err
	}
	applySearchAnalyzerToQuery(parsed)
	req := bleve.NewSearchRequestOptions(parsed, size, from, false)
	if len(fields) > 0 {
		req.Fields = fields
	}
	return idx.Search(req)
}
