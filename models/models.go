package models

import (
	"github.com/uptrace/bun"
)

// Slack export JSON types (for unmarshaling)

type SlackTopic struct {
	Value   string `json:"value"`
	Creator string `json:"creator"`
	LastSet int64  `json:"last_set"`
}

type SlackPurpose struct {
	Value   string `json:"value"`
	Creator string `json:"creator"`
	LastSet int64  `json:"last_set"`
}

type UserProfile struct {
	Title                 string `json:"title"`
	Phone                 string `json:"phone"`
	RealName              string `json:"real_name"`
	RealNameNormalized    string `json:"real_name_normalized"`
	DisplayName           string `json:"display_name"`
	DisplayNameNormalized string `json:"display_name_normalized"`
	Email                 string `json:"email"`
	FirstName             string `json:"first_name"`
	LastName              string `json:"last_name"`
	ImageOriginal         string `json:"image_original"`
	Image24               string `json:"image_24"`
	Image32               string `json:"image_32"`
	Image48               string `json:"image_48"`
	Image72               string `json:"image_72"`
	Image192              string `json:"image_192"`
	Image512              string `json:"image_512"`
	Team                  string `json:"team"`
}

type User struct {
	ID        string      `json:"id"`
	TeamID    string      `json:"team_id"`
	Name      string      `json:"name"`
	Deleted   bool        `json:"deleted"`
	Profile   UserProfile `json:"profile"`
	IsBot     bool        `json:"is_bot"`
	IsAppUser bool        `json:"is_app_user"`
	Updated   int64       `json:"updated"`
}

type Channel struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Created    int64        `json:"created"`
	Creator    string       `json:"creator"`
	IsArchived bool         `json:"is_archived"`
	IsGeneral  bool         `json:"is_general"`
	Members    []string     `json:"members"`
	Topic      SlackTopic   `json:"topic"`
	Purpose    SlackPurpose `json:"purpose"`
}

type Group struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Created    int64        `json:"created"`
	Creator    string       `json:"creator"`
	IsArchived bool         `json:"is_archived"`
	Members    []string     `json:"members"`
	Topic      SlackTopic   `json:"topic"`
	Purpose    SlackPurpose `json:"purpose"`
}

type DM struct {
	ID      string   `json:"id"`
	Created int64    `json:"created"`
	Members []string `json:"members"`
}

type MPIM struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Created    int64        `json:"created"`
	Creator    string       `json:"creator"`
	IsArchived bool         `json:"is_archived"`
	Members    []string     `json:"members"`
	Topic      SlackTopic   `json:"topic"`
	Purpose    SlackPurpose `json:"purpose"`
}

// MessageUserProfile appears in each message in export
type MessageUserProfile struct {
	AvatarHash        string `json:"avatar_hash"`
	Image72           string `json:"image_72"`
	FirstName         string `json:"first_name"`
	RealName          string `json:"real_name"`
	DisplayName       string `json:"display_name"`
	Team              string `json:"team"`
	Name              string `json:"name"`
	IsRestricted      bool   `json:"is_restricted"`
	IsUltraRestricted bool   `json:"is_ultra_restricted"`
}

// MessageFile is a file attached to a message (Slack export files[] element).
type MessageFile struct {
	ID         string `json:"id"`
	Created    int64  `json:"created"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Mimetype   string `json:"mimetype"`
	Filetype   string `json:"filetype"`
	Size       int64  `json:"size"`
	URLPrivate string `json:"url_private"`
}

// MessageAttachment is an attachment on a message (Slack export attachments[] element). We store "text" and "pretext".
type MessageAttachment struct {
	Text    string `json:"text"`
	Pretext string `json:"pretext"`
}

// Message blocks (Slack Block Kit). Stored as raw interface slice so we can walk
// rich_text blocks and render to HTML without defining every block/element type.
type Message struct {
	User        string                `json:"user"`
	Type        string                `json:"type"`
	Ts          string                `json:"ts"`
	ClientMsgID string                `json:"client_msg_id"`
	Text        string                `json:"text"`
	Team        string                `json:"team"`
	UserTeam    string                `json:"user_team"`
	SourceTeam  string                `json:"source_team"`
	UserProfile *MessageUserProfile   `json:"user_profile"`
	Blocks      []interface{}         `json:"blocks"`
	Files       []MessageFile         `json:"files"`
	Attachments []MessageAttachment   `json:"attachments"`
}

// Normalized database table models (Bun)

type UserRow struct {
	bun.BaseModel `bun:"table:users"`
	ID            string `bun:"id,pk"`
	TeamID        string `bun:"team_id"`
	Name          string `bun:"name"`
	Deleted       bool   `bun:"deleted"`
	RealName      string `bun:"real_name"`
	DisplayName   string `bun:"display_name"`
	Email         string `bun:"email"`
	IsBot         bool   `bun:"is_bot"`
	IsAppUser     bool   `bun:"is_app_user"`
	Updated       int64  `bun:"updated"`
}

type ChannelRow struct {
	bun.BaseModel  `bun:"table:channels"`
	ID             string `bun:"id,pk"`
	Name           string `bun:"name"`
	Created        int64  `bun:"created"`
	Creator        string `bun:"creator"`
	IsArchived     bool   `bun:"is_archived"`
	IsGeneral      bool   `bun:"is_general"`
	TopicValue     string `bun:"topic_value"`
	TopicCreator   string `bun:"topic_creator"`
	TopicLastSet   int64  `bun:"topic_last_set"`
	PurposeValue   string `bun:"purpose_value"`
	PurposeCreator string `bun:"purpose_creator"`
	PurposeLastSet int64  `bun:"purpose_last_set"`
}

type ChannelMemberRow struct {
	bun.BaseModel `bun:"table:channel_members"`
	ChannelID     string `bun:"channel_id,pk"`
	UserID        string `bun:"user_id,pk"`
}

type GroupRow struct {
	bun.BaseModel  `bun:"table:groups"`
	ID             string `bun:"id,pk"`
	Name           string `bun:"name"`
	Created        int64  `bun:"created"`
	Creator        string `bun:"creator"`
	IsArchived     bool   `bun:"is_archived"`
	TopicValue     string `bun:"topic_value"`
	TopicCreator   string `bun:"topic_creator"`
	TopicLastSet   int64  `bun:"topic_last_set"`
	PurposeValue   string `bun:"purpose_value"`
	PurposeCreator string `bun:"purpose_creator"`
	PurposeLastSet int64  `bun:"purpose_last_set"`
}

type GroupMemberRow struct {
	bun.BaseModel `bun:"table:group_members"`
	GroupID       string `bun:"group_id,pk"`
	UserID        string `bun:"user_id,pk"`
}

type DMRow struct {
	bun.BaseModel `bun:"table:dms"`
	ID            string `bun:"id,pk"`
	Created       int64  `bun:"created"`
}

type DMMemberRow struct {
	bun.BaseModel `bun:"table:dm_members"`
	DMID          string `bun:"dm_id,pk"`
	UserID        string `bun:"user_id,pk"`
}

type MPIMRow struct {
	bun.BaseModel  `bun:"table:mpims"`
	ID             string `bun:"id,pk"`
	Name           string `bun:"name"`
	Created        int64  `bun:"created"`
	Creator        string `bun:"creator"`
	IsArchived     bool   `bun:"is_archived"`
	TopicValue     string `bun:"topic_value"`
	TopicCreator   string `bun:"topic_creator"`
	TopicLastSet   int64  `bun:"topic_last_set"`
	PurposeValue   string `bun:"purpose_value"`
	PurposeCreator string `bun:"purpose_creator"`
	PurposeLastSet int64  `bun:"purpose_last_set"`
}

type MPIMMemberRow struct {
	bun.BaseModel `bun:"table:mpim_members"`
	MPIMID        string `bun:"mpim_id,pk"`
	UserID        string `bun:"user_id,pk"`
}

type MessageRow struct {
	bun.BaseModel    `bun:"table:messages"`
	ID               int64  `bun:"id,autoincrement,pk"`
	ConversationID   string `bun:"conversation_id"`
	ConversationType string `bun:"conversation_type"` // "channel" | "group" | "dm" | "mpim"
	UserID           string `bun:"user_id"`
	Type             string `bun:"type"`
	Ts               string `bun:"ts"`
	ClientMsgID      string `bun:"client_msg_id"`
	Text             string `bun:"text"`
	UserProfileName  string `bun:"user_profile_name"`
	Team             string `bun:"team"`
	UserTeam         string `bun:"user_team"`
	SourceTeam       string `bun:"source_team"`
}

// MessageFileRow stores a file attached to a message. Foreign key: (message_conversation_id, message_ts) references messages(conversation_id, ts).
// SlackFileID is the Slack file id (e.g. F06GYV87KE3) for deduplication when the same message appears multiple times in export.
type MessageFileRow struct {
	bun.BaseModel         `bun:"table:message_files"`
	ID                    int64  `bun:"id,autoincrement,pk"`
	MessageConversationID string `bun:"message_conversation_id"`
	MessageTs             string `bun:"message_ts"`
	SlackFileID           string `bun:"slack_file_id"` // Slack file id for unique constraint with message
	URLPrivate            string `bun:"url_private"`
	Name                  string `bun:"name"`
	Mimetype              string `bun:"mimetype"`
	Filetype              string `bun:"filetype"`
	Size                  int64  `bun:"size"`
}

// MessageAttachmentRow stores an attachment on a message. Foreign key: (message_conversation_id, message_ts) references messages(conversation_id, ts).
// Stores the attachment "text" and "pretext" from the JSON. Position is the 0-based index in the message's attachments array (for deduplication).
type MessageAttachmentRow struct {
	bun.BaseModel          `bun:"table:message_attachments"`
	ID                     int64  `bun:"id,autoincrement,pk"`
	MessageConversationID  string `bun:"message_conversation_id"`
	MessageTs              string `bun:"message_ts"`
	Position               int    `bun:"position"` // 0-based index in attachments[]
	Text                   string `bun:"text"`
	Pretext                string `bun:"pretext"`
}

// SearchDocument is the shape of a document indexed in Bleve (includes user_profile name for mapping)
type SearchDocument struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversation_id"`
	ConversationType string `json:"conversation_type"`
	UserID           string `json:"user_id"`
	Type             string `json:"type"`
	Ts               string `json:"ts"`
	Text             string `json:"text"`
	UserProfileName  string `json:"name"` // Bleve mapping: "name" field of user_profile
	Team             string `json:"team"`
}
