package entity

import (
	"encoding/json"
	"time"
)

// SmartSender API entities

// StringID is a helper type that accepts JSON numbers or strings and stores them as string.
// This avoids errors when the remote API sometimes emits numeric ids instead of quoted strings.
type StringID string

func (s *StringID) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	// quoted string
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = StringID(str)
		return nil
	}
	// number (or other) -> use json.Number to preserve representation
	var num json.Number
	if err := json.Unmarshal(b, &num); err != nil {
		// fallback: try unmarshaling as string
		var str string
		if err2 := json.Unmarshal(b, &str); err2 != nil {
			return err
		}
		*s = StringID(str)
		return nil
	}
	*s = StringID(num.String())
	return nil
}

// SSChat represents a chat from SmartSender API
type SSChat struct {
	ID      StringID  `json:"id"`
	Contact SSContact `json:"contact"`
}

// SSContact represents a contact within a SmartSender chat
type SSContact struct {
	OriginalID string `json:"originalId"`
	FullName   string `json:"fullName,omitempty"`
}

// SSChatResponse represents the paginated response from SmartSender chats API
type SSChatResponse struct {
	Collection []SSChat `json:"collection"`
	Cursor     SSCursor `json:"cursor"`
}

// SSCursor represents pagination cursor from SmartSender API
type SSCursor struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

// SSMessage represents a message from SmartSender API
type SSMessage struct {
	ID        StringID         `json:"id"`
	CreatedAt time.Time        `json:"createdAt"`
	Content   SSMessageContent `json:"content"`
	Sender    SSSender         `json:"sender"`
}

// SSMessageContent represents the content structure of a message
type SSMessageContent struct {
	Type     string            `json:"type"`
	Resource SSMessageResource `json:"resource,omitempty"`
}

// SSMessageResource represents the resource within message content
type SSMessageResource struct {
	Parameters SSMessageParams `json:"parameters,omitempty"`
}

// SSMessageParams contains the actual message parameters
type SSMessageParams struct {
	Content string `json:"content,omitempty"`
}

// SSSender represents the sender of a message
type SSSender struct {
	FullName string `json:"fullName,omitempty"`
}

// SSMessageResponse represents the paginated response from SmartSender messages API
type SSMessageResponse struct {
	Collection []SSMessage `json:"collection"`
	Cursor     SSCursor    `json:"cursor"`
}

// ZohoMessagePayload represents the payload sent to Zoho for new messages
type ZohoMessagePayload struct {
	ContactID string            `json:"contact_id"`
	Messages  []ZohoMessageItem `json:"messages"`
}

// ZohoMessageItem represents a single message item for Zoho
type ZohoMessageItem struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
	Content   string `json:"content"`
	Sender    string `json:"sender"`
}
