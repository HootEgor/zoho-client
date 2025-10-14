package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/lib/util"
)

type ZohoService struct {
	clientID     string
	clientSecret string
	refreshToken string
	initialToken string
	refreshUrl   string
	tokenExpiry  time.Time
	crmUrl       string
	scope        string
	apiVersion   string
	log          *slog.Logger
}

func NewZohoService(conf *config.Config, log *slog.Logger) (*ZohoService, error) {

	service := &ZohoService{
		clientID:     conf.Zoho.ClientId,
		clientSecret: conf.Zoho.ClientSecret,
		initialToken: conf.Zoho.RefreshToken,
		refreshUrl:   conf.Zoho.RefreshUrl,
		crmUrl:       conf.Zoho.CrmUrl,
		scope:        conf.Zoho.Scope,
		apiVersion:   conf.Zoho.ApiVersion,
		log:          log.With(sl.Module("zoho")),
	}

	return service, nil
}

func (s *ZohoService) RefreshToken() error {

	if s.refreshToken != "" && time.Now().Before(s.tokenExpiry) {
		return nil
	}
	var err error
	for i := 0; i < 3; i++ {
		if err = s.requestToken(); err == nil {
			break
		}
		s.log.With(
			slog.Int("attempt", i+1),
			sl.Err(err),
		).Warn("refresh token failed")
		time.Sleep(30 * time.Second)
	}

	return nil
}

func (s *ZohoService) requestToken() error {
	form := url.Values{}
	form.Add("client_id", s.clientID)
	form.Add("client_secret", s.clientSecret)
	form.Add("refresh_token", s.initialToken)
	form.Add("grant_type", "refresh_token")

	resp, err := http.PostForm(s.refreshUrl, form)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("refresh token failed: %s", string(bodyBytes))
	}

	var response entity.TokenResponse
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	s.refreshToken = response.AccessToken

	if response.AccessToken == "" {
		s.log.With(
			slog.Any("response", response),
		).Debug("refresh token failed")
		return fmt.Errorf("empty access token")
	}
	if response.ApiDomain != "" {
		s.crmUrl = response.ApiDomain
	}
	if response.ExpiresIn != 0 {
		s.tokenExpiry = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}

	s.log.With(
		slog.Time("expires", s.tokenExpiry),
		sl.Secret("token", response.AccessToken),
	).Debug("refresh token succeeded")

	return nil
}

func (s *ZohoService) CreateContact(contact *entity.ClientDetails) (string, error) {

	log := s.log.With(
		slog.String("email", contact.Email),
		slog.String("phone", contact.Phone),
		slog.String("name", fmt.Sprintf("%s %s", contact.FirstName, contact.LastName)),
	)

	err := util.ValidateEmail(contact.Email)
	if err != nil {
		log.Debug("invalid email")
		contact.Email = ""
	}

	if contact.Email == "" && contact.Phone == "" {
		return "", fmt.Errorf("email and phone are empty")
	}

	payload := map[string]interface{}{
		"data": []*entity.Contact{
			{
				Email:     contact.Email,
				Phone:     contact.Phone,
				FirstName: contact.FirstName,
				LastName:  contact.LastName,
				Field2:    contact.City,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	fullURL, err := buildURL(s.crmUrl, s.scope, s.apiVersion, "Contacts")
	if err != nil {
		return "", err
	}

	if e := s.RefreshToken(); e != nil {
		return "", e
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
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	//s.log.With(
	//	slog.String("response", string(bodyBytes)),
	//).Debug("create contact response")

	var apiResp entity.ZohoAPIResponse
	if err = json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return "", fmt.Errorf("empty response data")
	}

	item := apiResp.Data[0]

	// Handle DUPLICATE_DATA gracefully
	if item.Status == "error" {
		id := ""
		if item.Code == "DUPLICATE_DATA" {
			var dup entity.DuplicateDetails
			if err = json.Unmarshal(item.Details, &dup); err != nil {
				return "", fmt.Errorf("failed to parse duplicate details: %w", err)
			}
			id = dup.DuplicateRecord.ID
			//log.With(
			//	slog.String("duplicate_id", dup.DuplicateRecord.ID),
			//	slog.String("owner", dup.DuplicateRecord.Owner.Name),
			//	slog.String("module", dup.DuplicateRecord.Module.APIName),
			//).Debug("duplicate record detected")
			if id != "" {
				return id, nil
			}
		}

		if item.Code == "MULTIPLE_OR_MULTI_ERRORS" {
			var multiErr entity.MultipleErrors
			if err = json.Unmarshal(item.Details, &multiErr); err != nil {
				return "", fmt.Errorf("failed to parse multiple errors: %w", err)
			}
			id = multiErr.Errors[0].Details.DuplicateRecord.ID
			//log.With(
			//	slog.Any("error_message", multiErr.Errors[0].Message),
			//	slog.String("duplicate_id", id),
			//).Debug("multiple errors detected")
			if id != "" {
				return id, nil
			}
		}
		return "", fmt.Errorf("zoho error [%s]: %s", item.Code, item.Message)
	}

	// Success path: extract the record ID
	var successDetails entity.SuccessContactDetails
	if err = json.Unmarshal(item.Details, &successDetails); err != nil {
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

	if e := s.RefreshToken(); e != nil {
		return "", e
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
		//slog.String("payload", string(body)),
	)
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
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	//s.log.With(
	//	slog.String("response", string(bodyBytes)),
	//).Debug("create order response")

	var apiResp entity.ZohoAPIResponse
	if err = json.Unmarshal(bodyBytes, &apiResp); err != nil {
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
	if err = json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse order ID: %w", err)
	}

	s.log.With(
		slog.String("id", success.ID),
		slog.String("subject", orderData.Subject),
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

	if e := s.RefreshToken(); e != nil {
		return e
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
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

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
