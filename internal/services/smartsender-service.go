package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"
)

type SmartSenderService struct {
	apiKey     string
	baseURL    string
	log        *slog.Logger
	httpClient *http.Client
}

func NewSmartSenderService(conf *config.Config, log *slog.Logger) (*SmartSenderService, error) {
	if !conf.SmartSender.Enabled {
		return nil, nil
	}

	if conf.SmartSender.ApiKey == "" {
		return nil, fmt.Errorf("smartsender api_key is required")
	}

	service := &SmartSenderService{
		apiKey:  conf.SmartSender.ApiKey,
		baseURL: conf.SmartSender.BaseURL,
		log:     log.With(sl.Module("smartsender")),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}

	return service, nil
}

// GetAllChats fetches all chats from SmartSender API with pagination
func (s *SmartSenderService) GetAllChats() ([]entity.SSChat, error) {
	var allChats []entity.SSChat
	page := 1
	limit := 20

	for {
		url := fmt.Sprintf("%s/chats?page=%d&limitation=%d", s.baseURL, page, limit)

		resp, err := s.doRequest(http.MethodGet, url)
		if err != nil {
			return nil, fmt.Errorf("fetch chats page %d: %w", page, err)
		}

		var chatResp entity.SSChatResponse
		if err := json.Unmarshal(resp, &chatResp); err != nil {
			return nil, fmt.Errorf("decode chats response: %w", err)
		}

		allChats = append(allChats, chatResp.Collection...)

		if chatResp.Cursor.Page >= chatResp.Cursor.Pages {
			break
		}
		page++
	}

	//s.log.Debug("fetched all chats", slog.Int("count", len(allChats)))
	return allChats, nil
}

// GetMessages fetches messages for a specific chat
func (s *SmartSenderService) GetMessages(chatID string, limit int) ([]entity.SSMessage, error) {
	url := fmt.Sprintf("%s/chats/%s/messages?limitation=%d&page=1", s.baseURL, chatID, limit)

	resp, err := s.doRequest(http.MethodGet, url)
	if err != nil {
		return nil, fmt.Errorf("fetch messages for chat %s: %w", chatID, err)
	}

	var msgResp entity.SSMessageResponse
	if err := json.Unmarshal(resp, &msgResp); err != nil {
		return nil, fmt.Errorf("decode messages response: %w", err)
	}

	return msgResp.Collection, nil
}

// GetMessagesAfterTime fetches messages for a chat that were created after the specified time
func (s *SmartSenderService) GetMessagesAfterTime(chatID string, afterTime time.Time) ([]entity.SSMessage, error) {
	// Fetch up to 100 messages to ensure we get all recent ones
	messages, err := s.GetMessages(chatID, 100)
	if err != nil {
		return nil, err
	}

	// Filter messages created after the specified time
	var filteredMessages []entity.SSMessage
	for _, msg := range messages {
		if msg.CreatedAt.After(afterTime) {
			filteredMessages = append(filteredMessages, msg)
		}
	}

	return filteredMessages, nil
}

func (s *SmartSenderService) doRequest(method, url string) ([]byte, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		if closeErr := Body.Close(); closeErr != nil {
			s.log.With(sl.Err(closeErr)).Warn("failed to close response body")
		}
	}(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	return bodyBytes, nil
}
