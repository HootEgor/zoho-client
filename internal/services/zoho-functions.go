package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/httputil"
	"zohoclient/internal/lib/sl"
)

// ZohoFunctionsService calls custom Zoho CRM server-side functions via the Functions API.
// Used to push SmartSender chat messages into Zoho CRM.
// Auth: API key-based (zapikey query parameter).
// Ref: https://www.zoho.com/crm/developer/docs/functions/
type ZohoFunctionsService struct {
	apiKey     string
	msgURL     string
	log        *slog.Logger
	httpClient *http.Client
}

func NewZohoFunctionsService(conf *config.Config, log *slog.Logger) (*ZohoFunctionsService, error) {
	if !conf.SmartSender.Enabled {
		return nil, nil
	}

	if conf.SmartSender.ZohoApiKey == "" {
		return nil, fmt.Errorf("zoho_api_key is required for SmartSender integration")
	}

	service := &ZohoFunctionsService{
		apiKey:     conf.SmartSender.ZohoApiKey,
		msgURL:     conf.SmartSender.ZohoMsgURL,
		log:        log.With(sl.Module("zoho-func")),
		httpClient: httputil.NewHTTPClient(30 * time.Second),
	}

	return service, nil
}

// SendMessages sends chat messages to Zoho CRM via the custom "getmessagefromsmartsender"
// server-side function. Authenticated with zapikey query parameter.
func (s *ZohoFunctionsService) SendMessages(contactID string, messages []entity.ZohoMessageItem) error {
	if len(messages) == 0 {
		return nil
	}

	payload := entity.ZohoMessagePayload{
		ContactID: contactID,
		Messages:  messages,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal message payload: %w", err)
	}

	if err := s.doRequest(body); err != nil {
		return fmt.Errorf("send messages to Zoho: %w", err)
	}

	//s.log.Debug("messages sent to Zoho",
	//	slog.String("contact_id", contactID),
	//	slog.Int("count", len(messages)),
	//)

	return nil
}

func (s *ZohoFunctionsService) doRequest(body []byte) error {
	url := fmt.Sprintf("%s?auth_type=apikey&zapikey=%s", s.msgURL, s.apiKey)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer httputil.CloseBody(resp.Body, s.log)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
