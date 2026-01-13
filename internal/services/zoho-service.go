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
	httpClient   *http.Client
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

func (s *ZohoService) RefreshToken() error {
	if s.refreshToken != "" && time.Now().Before(s.tokenExpiry) {
		return nil
	}
	var err error
	for i := 0; i < 3; i++ {
		if err = s.requestToken(); err == nil {
			return nil
		}
		s.log.With(
			slog.Int("attempt", i+1),
			sl.Err(err),
		).Warn("refresh token failed")
		if i < 2 {
			time.Sleep(30 * time.Second)
		}
	}

	return fmt.Errorf("refresh token failed after 3 attempts: %w", err)
}

func (s *ZohoService) requestToken() error {
	form := url.Values{}
	form.Add("client_id", s.clientID)
	form.Add("client_secret", s.clientSecret)
	form.Add("refresh_token", s.initialToken)
	form.Add("grant_type", "refresh_token")

	resp, err := s.httpClient.PostForm(s.refreshUrl, form)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			s.log.With(sl.Err(err)).Warn("failed to close response body")
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("refresh token failed (status %d), failed to read body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("refresh token failed: %s", string(bodyBytes))
	}

	var response entity.TokenResponse
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	s.refreshToken = response.AccessToken

	if response.AccessToken == "" {
		s.log.With(slog.Any("response", response)).Debug("refresh token failed")
		return fmt.Errorf("empty access token")
	}
	if response.ApiDomain != "" {
		s.crmUrl = response.ApiDomain
	}
	if response.ExpiresIn != 0 {
		s.tokenExpiry = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}

	return nil
}

func (s *ZohoService) CreateContact(contact *entity.ClientDetails) (string, error) {

	log := s.log.With(
		slog.String("email", contact.Email),
		slog.String("phone", contact.Phone),
		slog.String("name", fmt.Sprintf("%s : %s", contact.FirstName, contact.LastName)),
	)

	err := util.ValidateEmail(contact.Email)
	if err != nil {
		log.Debug("invalid email")
		contact.Email = ""
	}

	if contact.Email == "" && contact.Phone == "" {
		return "", fmt.Errorf("email and phone are empty")
	}

	if contact.FirstName == "" {
		contact.FirstName = "?"
	}
	if contact.LastName == "" {
		contact.LastName = "?"
	}

	// Specify duplicate check fields so upsert updates existing contacts
	// instead of returning DUPLICATE_DATA error when Phone matches
	duplicateCheckFields := []string{}
	if contact.Email != "" {
		duplicateCheckFields = append(duplicateCheckFields, "Email")
	}
	if contact.Phone != "" {
		duplicateCheckFields = append(duplicateCheckFields, "Phone")
	}

	payload := map[string]interface{}{
		"data": []*entity.Contact{
			{
				Email:     contact.Email,
				Phone:     contact.Phone,
				FirstName: contact.FirstName,
				LastName:  contact.LastName,
				City:      contact.City,
				Country:   contact.Country,
			},
		},
		"duplicate_check_fields": duplicateCheckFields,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(http.MethodPost, body, "Contacts", "upsert")
	if err != nil {
		return "", err
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		return "", fmt.Errorf("zoho error: %s", item)
	}

	// Log whether contact was created or updated
	if item.Action == "update" {
		log.Debug("contact updated via upsert", slog.String("duplicate_field", item.DuplicateField))
	} else {
		log.Debug("contact created via upsert")
	}

	// Extract the record ID
	var successDetails entity.SuccessContactDetails
	if err = json.Unmarshal(item.Details, &successDetails); err != nil {
		return "", fmt.Errorf("failed to parse success ID: %w", err)
	}

	return successDetails.ID, nil

}

func (s *ZohoService) CreateOrder(orderData entity.ZohoOrder) (string, error) {
	log := s.log.With(
		slog.String("subject", orderData.Subject),
		slog.Float64("vat", orderData.VAT),
		slog.Float64("discount", orderData.DiscountP),
		slog.Float64("coupon", orderData.CouponValue),
		slog.Float64("sub_total", orderData.SubTotal),
		slog.Float64("total", orderData.GrandTotal),
	)
	t := time.Now()
	var err error
	defer func() {
		log = log.With(slog.Duration("duration", time.Since(t)))
		if err != nil {
			log.With(
				sl.Err(err),
			).Error("order not created")
		}
	}()

	payload := map[string]interface{}{
		"data": []entity.ZohoOrder{orderData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(http.MethodPost, body, "Sales_Orders")
	if err != nil {
		return "", err
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		// Decode error details
		var errDetails entity.ErrorDetails
		_ = json.Unmarshal(item.Details, &errDetails)

		err = fmt.Errorf(
			"order not created: [%s] %s (field: %s, path: %s)",
			item.Code,
			item.Message,
			errDetails.APIName,
			errDetails.JSONPath,
		)
		return "", err
	}

	// Decode success
	var success entity.SuccessOrderDetails
	if err = json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse order ID: %w", err)
	}

	log = log.With(
		slog.String("id", success.ID),
	)

	return success.ID, nil

}

func (s *ZohoService) CreateB2BOrder(orderData entity.ZohoOrderB2B) (string, error) {
	log := s.log.With(
		slog.String("subject", orderData.Subject),
		slog.Float64("vat", orderData.VAT),
		slog.Float64("discount", orderData.DiscountP),
	)

	if orderData.GrandTotalUAH > 0 {
		log = log.With(
			slog.Float64("total_UAH", orderData.GrandTotalUAH),
			slog.Float64("sub_total_UAH", orderData.GrandTotalUAH),
		)
	} else if orderData.GrandTotalPLN > 0 {
		log = log.With(
			slog.Float64("total_PLN", orderData.GrandTotalPLN),
			slog.Float64("sub_total_PLN", orderData.GrandTotalPLN),
		)
	} else if orderData.GrandTotalUSD > 0 {
		log = log.With(
			slog.Float64("total_USD", orderData.GrandTotalUSD),
			slog.Float64("sub_total_USD", orderData.GrandTotalUSD),
		)
	} else if orderData.GrandTotalEUR > 0 {
		log = log.With(
			slog.Float64("total_EUR", orderData.GrandTotalEUR),
			slog.Float64("sub_total_EUR", orderData.GrandTotalEUR),
		)
	}

	t := time.Now()
	var err error
	defer func() {
		log = log.With(slog.Duration("duration", time.Since(t)))
		if err != nil {
			log.With(
				sl.Err(err),
			).Error("order not created")
		}
	}()

	payload := map[string]interface{}{
		"data": []entity.ZohoOrderB2B{orderData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(http.MethodPost, body, "Deals")
	if err != nil {
		return "", err
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		// Decode error details
		var errDetails entity.ErrorDetails
		_ = json.Unmarshal(item.Details, &errDetails)

		err = fmt.Errorf(
			"order not created: [%s] %s (field: %s, path: %s)",
			item.Code,
			item.Message,
			errDetails.APIName,
			errDetails.JSONPath,
		)
		return "", err
	}

	// Decode success
	var success entity.SuccessOrderDetails
	if err = json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse order ID: %w", err)
	}

	log = log.With(
		slog.String("id", success.ID),
	)

	log.Debug("B2B order created successfully")

	return success.ID, nil

}

func (s *ZohoService) AddItemsToOrder(orderID string, items []*entity.OrderedItem) (string, error) {
	// This is based on the user's sample input for updating a subform.
	// We create a payload for a bulk/mass update, but only for a single record.
	updateData := map[string]interface{}{
		"id":            orderID,
		"Ordered_Items": items,
	}

	payload := map[string]interface{}{
		"data": []interface{}{updateData},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(http.MethodPut, body, "Sales_Orders")
	if err != nil {
		return "", err
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		var errDetails entity.ErrorDetails
		_ = json.Unmarshal(item.Details, &errDetails)
		return "", fmt.Errorf(
			"items not added: [%s] %s (field: %s, path: %s)",
			item.Code,
			item.Message,
			errDetails.APIName,
			errDetails.JSONPath,
		)
	}

	var success entity.SuccessOrderDetails
	if err := json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse success response: %w", err)
	}

	return success.ID, nil
}

func (s *ZohoService) AddItemsToOrderB2B(_ string, items []*entity.Good) (string, error) {
	payload := map[string]interface{}{
		"data": items,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	s.log.With(slog.String("body", fmt.Sprintf("%s", body))).Debug("Goods payload")

	apiResp, err := s.doRequest(http.MethodPost, body, "Goods")
	if err != nil {
		return "", err
	}

	s.log.With(
		slog.String("body", fmt.Sprintf("%s", apiResp)),
	).Debug("goods response")

	item := apiResp.Data[0]

	if item.Status != "success" {
		var errDetails entity.ErrorDetails
		_ = json.Unmarshal(item.Details, &errDetails)
		return "", fmt.Errorf(
			"items not added: [%s] %s (field: %s, path: %s)",
			item.Code,
			item.Message,
			errDetails.APIName,
			errDetails.JSONPath,
		)
	}

	var success entity.SuccessOrderDetails
	if err := json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse success response: %w", err)
	}

	return success.ID, nil
}

func (s *ZohoService) UpdateOrder(orderData entity.ZohoOrder, id string) error {
	payload := map[string]interface{}{
		"data": []entity.ZohoOrder{orderData},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(http.MethodPut, body, "Sales_Orders", id)
	if err != nil {
		return err
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

func (s *ZohoService) doRequest(method string, body []byte, pathSegments ...string) (*entity.ZohoAPIResponse, error) {
	segments := append([]string{s.scope, s.apiVersion}, pathSegments...)
	fullURL, err := buildURL(s.crmUrl, segments...)
	if err != nil {
		return nil, err
	}

	if err = s.RefreshToken(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, fullURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+s.refreshToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			s.log.With(sl.Err(err)).Warn("failed to close response body")
		}
	}(resp.Body)

	// Check for rate limiting (v8 API has stricter limits)
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		return nil, fmt.Errorf("rate limited by Zoho API, retry after: %s", retryAfter)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var apiResp entity.ZohoAPIResponse
	if err = json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("empty response data")
	}

	return &apiResp, nil
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
