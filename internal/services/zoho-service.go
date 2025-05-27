package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"
)

type ZohoService struct {
	clientID     string
	clientSecret string
	refreshToken string
	refreshUrl   string
	crmUrl       string
	scope        string
	apiVersion   string
	log          *slog.Logger
}

func NewZohoService(conf *config.Config, log *slog.Logger) (*ZohoService, error) {

	service := &ZohoService{
		clientID:     conf.Zoho.ClientId,
		clientSecret: conf.Zoho.ClientSecret,
		refreshToken: conf.Zoho.RefreshToken,
		refreshUrl:   conf.Zoho.RefreshUrl,
		crmUrl:       conf.Zoho.CrmUrl,
		scope:        conf.Zoho.Scope,
		apiVersion:   conf.Zoho.ApiVersion,
		log:          log.With(sl.Module("zoho")),
	}

	return service, nil
}

func (s *ZohoService) RefreshToken() error {
	form := url.Values{}
	form.Add("client_id", s.clientID)
	form.Add("client_secret", s.clientSecret)
	form.Add("refresh_token", s.refreshToken)
	form.Add("grant_type", "refresh_token")

	resp, err := http.PostForm(s.refreshUrl, form)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("refresh token failed: %s", string(bodyBytes))
	}

	var response entity.TokenResponse
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	s.log.With(
		slog.Any("response", response),
	).Debug("refresh token succeeded")

	s.refreshToken = response.AccessToken
	if response.ApiDomain != "" {
		s.crmUrl = response.ApiDomain
	}

	return nil
}

func (s *ZohoService) CreateContact(contactData entity.Contact) (string, error) {
	// Ensure phone is numeric only
	re := regexp.MustCompile(`\D`)
	contactData.Phone = re.ReplaceAllString(contactData.Phone, "")

	// Prepare request body
	payload := map[string]interface{}{
		"data": []entity.Contact{contactData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	fullURL, err := buildURL(s.crmUrl, s.scope, s.apiVersion, "Contacts")
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fullURL,
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.refreshToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	s.log.With(
		slog.String("response", string(bodyBytes)),
	).Debug("create contact response")

	var apiResp entity.ZohoAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return "", fmt.Errorf("empty response data")
	}

	item := apiResp.Data[0]

	// Handle DUPLICATE_DATA gracefully
	if item.Status == "error" {
		if item.Code == "DUPLICATE_DATA" {
			var dup entity.DuplicateDetails
			if err := json.Unmarshal(item.Details, &dup); err != nil {
				return "", fmt.Errorf("failed to parse duplicate details: %w", err)
			}
			s.log.With(
				slog.String("duplicate_id", dup.DuplicateRecord.ID),
				slog.String("owner", dup.DuplicateRecord.Owner.Name),
				slog.String("module", dup.DuplicateRecord.Module.APIName),
			).Debug("duplicate record detected")
			return dup.DuplicateRecord.ID, nil
		}
		return "", fmt.Errorf("zoho error [%s]: %s", item.Code, item.Message)
	}

	// Success path: extract the record ID
	var successDetails entity.SuccessContactDetails
	if err := json.Unmarshal(item.Details, &successDetails); err != nil {
		return "", fmt.Errorf("failed to parse success ID: %w", err)
	}

	return successDetails.ID, nil

}

func (s *ZohoService) CreateOrder(orderData entity.ZohoOrder) (string, error) {
	// Prepare payload
	payload := map[string]interface{}{
		"data": []entity.ZohoOrder{orderData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	fullURL, err := buildURL(s.crmUrl, s.scope, s.apiVersion, "Sales_Orders")
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		fullURL,
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+s.refreshToken)
	req.Header.Set("Content-Type", "application/json")

	log := s.log.With(
		slog.String("url", fullURL),
		slog.String("method", req.Method),
		slog.String("payload", string(body)))
	t := time.Now()
	defer func() {
		log = log.With(slog.Duration("duration", time.Since(t)))
		if err != nil {
			log.Error("create order", sl.Err(err))
		} else {
			log.Debug("create order")
		}
	}()

	// Execute request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.With(
			sl.Err(err),
		).Debug("response")
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	s.log.With(
		slog.String("response", string(bodyBytes)),
	).Debug("create order response")

	var apiResp entity.ZohoAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return "", fmt.Errorf("empty response data")
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		// Decode error details
		var errDetails entity.ErrorDetails
		_ = json.Unmarshal(item.Details, &errDetails)

		return "", fmt.Errorf(
			"order not created: [%s] %s (field: %s, path: %s)",
			item.Code,
			item.Message,
			errDetails.APIName,
			errDetails.JSONPath,
		)
	}

	// Decode success
	var success entity.SuccessOrderDetails
	if err := json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse order ID: %w", err)
	}

	s.log.With(
		slog.Any("order response", success),
	).Debug("order created successfully")

	return success.ID, nil

}

func (s *ZohoService) UpdateOrder(orderData entity.ZohoOrder, id string) error {
	// Prepare payload
	payload := map[string]interface{}{
		"data": []entity.ZohoOrder{orderData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	fullURL, err := buildURL(s.crmUrl, s.scope, s.apiVersion, "Sales_Orders", id)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(
		http.MethodPut,
		fullURL,
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+s.refreshToken)
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	s.log.With(
		slog.String("response", string(bodyBytes)),
	).Debug("create order response")

	var apiResp entity.ZohoAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return fmt.Errorf("empty response data")
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		// Decode error details
		var errDetails entity.ErrorDetails
		_ = json.Unmarshal(item.Details, &errDetails)

		return fmt.Errorf(
			"order not created: [%s] %s (field: %s, path: %s)",
			item.Code,
			item.Message,
			errDetails.APIName,
			errDetails.JSONPath,
		)
	}

	// Decode success
	var success entity.SuccessOrderDetails
	if err := json.Unmarshal(item.Details, &success); err != nil {
		return fmt.Errorf("failed to parse order ID: %w", err)
	}

	s.log.With(
		slog.Any("order response", success),
	).Debug("order created successfully")

	return nil

}

func buildURL(base string, paths ...string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	// Join additional path segments cleanly
	allPaths := append([]string{u.Path}, paths...)
	u.Path = path.Join(allPaths...)

	return u.String(), nil
}
