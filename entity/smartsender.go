package entity

import "time"

// SmartSender API entities

// SSChat represents a chat from SmartSender API
type SSChat struct {
	ID      string    `json:"id"`
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
	ID        string           `json:"id"`
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
