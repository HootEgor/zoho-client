package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"strconv"
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

// APIError represents a non-200 response from SmartSender API and optional RetryAfter
type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.Status, e.Body)
}

// parseRetryAfter tries to parse Retry-After header; supports seconds or HTTP-date
func parseRetryAfter(h string) (time.Duration, error) {
	if h == "" {
		return 0, fmt.Errorf("empty")
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	// try http time parse
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0, nil
		}
		return d, nil
	}
	return 0, fmt.Errorf("unparsable")
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

// doRequest performs HTTP request with rate limiter, retries and exponential backoff on 429/423/5xx
func (s *SmartSenderService) doRequest(method, url string) ([]byte, error) {
	// Retry parameters
	const (
		maxRetries     = 5
		baseDelay      = 500 * time.Millisecond
		maxDelay       = 10 * time.Second
		jitterFraction = 0.2
	)

	ctx := context.Background()

	var lastErr error

	backoffDuration := func(attempt int) time.Duration {
		d := float64(baseDelay) * math.Pow(2, float64(attempt))
		if d > float64(maxDelay) {
			d = float64(maxDelay)
		}
		j := 1 - jitterFraction + rand.Float64()*(2*jitterFraction)
		return time.Duration(d * j)
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Rate limiter: wait for token
		if err := Acquire(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter: %w", err)
		}

		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.apiKey)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("send request: %w", err)
			if attempt == maxRetries {
				break
			}
			// network error -> backoff and retry
			time.Sleep(backoffDuration(attempt))
			continue
		}

		// ensure body closed
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			s.log.With(sl.Err(closeErr)).Warn("failed to close response body")
		}
		if readErr != nil {
			lastErr = fmt.Errorf("read response body: %w", readErr)
			if attempt == maxRetries {
				break
			}
			time.Sleep(backoffDuration(attempt))
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return bodyBytes, nil
		}

		apiErr := &APIError{Status: resp.StatusCode, Body: string(bodyBytes)}
		// try parse Retry-After header
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if d, err := parseRetryAfter(ra); err == nil {
				apiErr.RetryAfter = d
			}
		}

		// set sensible defaults when header is missing for specific codes
		if apiErr.RetryAfter == 0 {
			if resp.StatusCode == 423 {
				// SmartSender sometimes returns 423 with a message indicating seconds; default to 12 minutes
				apiErr.RetryAfter = 720 * time.Second
			} else if resp.StatusCode == 429 {
				// default short backoff for 429 when Retry-After is absent
				apiErr.RetryAfter = 5 * time.Second
			}
		}

		// Determine if response is retriable
		retriable := resp.StatusCode == 429 || resp.StatusCode == 423 || (resp.StatusCode >= 500 && resp.StatusCode <= 599)
		if !retriable {
			return nil, apiErr
		}

		// Retriable error
		lastErr = apiErr
		if attempt == maxRetries {
			break
		}

		if apiErr.RetryAfter > 0 {
			wait := apiErr.RetryAfter
			if wait > maxDelay {
				wait = maxDelay
			}
			time.Sleep(wait)
		} else {
			time.Sleep(backoffDuration(attempt))
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("request failed after retries")
}
