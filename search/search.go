package search

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/denmark/slack-site/models"
)

const indexDir = "slack.bleve"

// IndexPath returns the Bleve index path under outputDir.
func IndexPath(outputDir string) string {
	return filepath.Join(outputDir, indexDir)
}

// NewIndex creates a new Bleve index at outputDir/slack.bleve. Removes existing index at that path.
func NewIndex(outputDir string) (bleve.Index, error) {
	path := IndexPath(outputDir)
	_ = os.RemoveAll(path)
	mapping := slackIndexMapping()
	return bleve.New(path, mapping)
}

// slackIndexMapping builds an index mapping that includes the "name" field of user_profile (indexed and searchable).
func slackIndexMapping() *mapping.IndexMappingImpl {
	idxMapping := bleve.NewIndexMapping()
	docMapping := bleve.NewDocumentMapping()

	// Text fields
	textField := bleve.NewTextFieldMapping()
	textField.Analyzer = "en"
	docMapping.AddFieldMappingsAt("id", textField)
	docMapping.AddFieldMappingsAt("conversation_id", textField)
	docMapping.AddFieldMappingsAt("conversation_type", textField)
	docMapping.AddFieldMappingsAt("user_id", textField)
	docMapping.AddFieldMappingsAt("type", textField)
	docMapping.AddFieldMappingsAt("ts", textField)
	docMapping.AddFieldMappingsAt("text", textField)
	docMapping.AddFieldMappingsAt("team", textField)
	// "name" field of user_profile (mapping as specified in plan)
	docMapping.AddFieldMappingsAt("name", textField)

	idxMapping.DefaultMapping = docMapping
	return idxMapping
}

// SearchDocumentForMessage returns a search document for a message (for batch or single index).
// text is the message body to index (e.g. HTML-rendered from rich_text blocks or plain msg.Text).
func SearchDocumentForMessage(conversationID, conversationType, ts string, msg *models.Message, text string) *models.SearchDocument {
	name := ""
	if msg.UserProfile != nil {
		name = msg.UserProfile.Name
	}
	return &models.SearchDocument{
		ID:               conversationID + "_" + ts,
		ConversationID:   conversationID,
		ConversationType: conversationType,
		UserID:           msg.User,
		Type:             msg.Type,
		Ts:               msg.Ts,
		Text:             text,
		UserProfileName:  name,
		Team:             msg.Team,
	}
}

// IndexMessage indexes a single message document. text is the message body (e.g. HTML-rendered).
func IndexMessage(idx bleve.Index, conversationID, conversationType, ts string, msg *models.Message, text string) error {
	doc := SearchDocumentForMessage(conversationID, conversationType, ts, msg, text)
	return idx.Index(doc.ID, doc)
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
		"id":       u.ID,
		"name":     u.Name,
		"real_name": u.Profile.RealName,
		"display_name": u.Profile.DisplayName,
		"email":    u.Profile.Email,
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
	if idx == nil {
		return nil, fmt.Errorf("index is nil")
	}
	query := bleve.NewQueryStringQuery(q)
	req := bleve.NewSearchRequestOptions(query, size, from, false)
	return idx.Search(req)
}
