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
	"zohoclient/internal/lib/httputil"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/lib/util"
)

// ZohoService manages communication with the Zoho CRM REST API (v8).
// API docs: https://www.zoho.com/crm/developer/docs/api/v8/
//
// Authentication uses the OAuth 2.0 refresh token flow:
// https://www.zoho.com/crm/developer/docs/api/v8/refresh.html
//
// The service automatically refreshes the access token before each request
// and keeps it cached until expiry.
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
		httpClient:   httputil.NewHTTPClient(30 * time.Second),
	}

	return service, nil
}

// RefreshToken ensures a valid OAuth access token is available.
// Uses the refresh token grant type to obtain a new access token when expired.
// Retries up to 3 times with 30s delays on failure.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/refresh.html
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
	defer httputil.CloseBody(resp.Body, s.log)

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

// CreateContact creates or updates (upserts) a contact in the Zoho CRM Contacts module.
// Uses duplicate_check_fields to match on Email/Phone and return the existing record ID
// instead of failing with DUPLICATE_DATA.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/upsert-records.html
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

	// Handle DUPLICATE_DATA gracefully - extract existing contact ID
	if item.Status == "error" {
		if item.Code == "DUPLICATE_DATA" {
			var dup entity.DuplicateDetails
			if err = json.Unmarshal(item.Details, &dup); err != nil {
				return "", fmt.Errorf("failed to parse duplicate details: %w", err)
			}
			if dup.DuplicateRecord.ID != "" {
				log.Debug("duplicate contact found, using existing",
					slog.String("duplicate_id", dup.DuplicateRecord.ID),
					slog.String("api_name", dup.APIName),
				)
				return dup.DuplicateRecord.ID, nil
			}
		}

		if item.Code == "MULTIPLE_OR_MULTI_ERRORS" {
			var multiErr entity.MultipleErrors
			if err = json.Unmarshal(item.Details, &multiErr); err != nil {
				return "", fmt.Errorf("failed to parse multiple errors: %w", err)
			}
			if len(multiErr.Errors) > 0 && multiErr.Errors[0].Details.DuplicateRecord.ID != "" {
				id := multiErr.Errors[0].Details.DuplicateRecord.ID
				log.Debug("duplicate contact found via multi-error, using existing",
					slog.String("duplicate_id", id),
				)
				return id, nil
			}
		}

		return "", fmt.Errorf("zoho error: %s", item)
	}

	if item.Status != "success" {
		return "", fmt.Errorf("zoho error: %s", item)
	}

	// Extract the record ID
	var successDetails entity.SuccessContactDetails
	if err = json.Unmarshal(item.Details, &successDetails); err != nil {
		return "", fmt.Errorf("failed to parse success ID: %w", err)
	}

	return successDetails.ID, nil

}

// CreateOrder creates a Sales Order in the Zoho CRM Sales_Orders module.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/insert-records.html
// Module: Sales_Orders - https://www.zoho.com/crm/developer/docs/api/v8/modules-api.html
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
		err = formatZohoError("order not created", item)
		return "", err
	}

	id, err := extractRecordID(item)
	if err != nil {
		return "", err
	}
	log = log.With(slog.String("id", id))

	return id, nil

}

// CreateB2BOrder creates a Deal in the Zoho CRM Deals module for B2B orders.
// B2B orders use Deals (not Sales_Orders) because they follow a pipeline-based workflow.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/insert-records.html
// Module: Deals - uses Pipeline and Stage fields for B2B workflow.
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

	//s.log.With(slog.String("body", fmt.Sprintf("%s", body))).Debug("deal payload")

	apiResp, err := s.doRequest(http.MethodPost, body, "Deals")
	if err != nil {
		return "", err
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		err = formatZohoError("B2B order not created", item)
		return "", err
	}

	id, err := extractRecordID(item)
	if err != nil {
		return "", err
	}
	log = log.With(slog.String("id", id))
	log.Debug("B2B order created successfully")

	return id, nil

}

// CreatePayment creates a payment record in the Zoho CRM custom Payments module.
// The payment is linked to a Sales Order via the "Sells" lookup field.
// Stripe payment data (PaymentIntent ID, Checkout Session ID) is stored for reconciliation.
func (s *ZohoService) CreatePayment(payment entity.ZohoPayment) (string, error) {
	payload := map[string]interface{}{
		"data": []entity.ZohoPayment{payment},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(http.MethodPost, body, "Payments")
	if err != nil {
		return "", err
	}

	item := apiResp.Data[0]

	if item.Status != "success" {
		return "", formatZohoError("payment not created", item)
	}

	return extractRecordID(item)
}

// AddItemsToOrder appends line items to an existing Sales Order via bulk update.
// Used when an order has >200 items and must be split across multiple API calls.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/update-records.html
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
		return "", formatZohoError("items not added", item)
	}

	return extractRecordID(item)
}

// AddItemsToOrderB2B creates records in the custom "Goods" module linked to a B2B Deal.
// Each Good references a Product and a Deal via lookup fields.
func (s *ZohoService) AddItemsToOrderB2B(_ string, items []*entity.Good) (string, error) {
	payload := map[string]interface{}{
		"data": items,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	//s.log.With(slog.String("body", fmt.Sprintf("%s", body))).Debug("Goods payload")

	apiResp, err := s.doRequest(http.MethodPost, body, "Goods")
	if err != nil {
		return "", err
	}

	//s.log.With(
	//	slog.String("body", fmt.Sprintf("%s", apiResp)),
	//).Debug("goods response")

	item := apiResp.Data[0]

	if item.Status != "success" {
		return "", formatZohoError("items not added", item)
	}

	return extractRecordID(item)
}

// UpdateOrder updates an existing Sales Order record by its Zoho record ID.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/update-specific-record.html
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
		return formatZohoError("order not updated", item)
	}

	recordID, err := extractRecordID(item)
	if err != nil {
		return err
	}

	s.log.With(slog.String("id", recordID)).Debug("order updated successfully")

	return nil

}

// doRequest executes an authenticated request against the Zoho CRM v8 REST API.
// It automatically refreshes the OAuth token, constructs the full URL from path segments
// (e.g., "Sales_Orders", "upsert"), and handles rate-limit (429) responses.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/api-limits.html
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
	defer httputil.CloseBody(resp.Body, s.log)

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

// formatZohoError decodes error details from a failed Zoho API response item
// and returns a formatted error with the error code, message, and field path.
func formatZohoError(context string, item entity.ZohoResponseItem) error {
	var errDetails entity.ErrorDetails
	_ = json.Unmarshal(item.Details, &errDetails)
	return fmt.Errorf(
		"%s: [%s] %s (field: %s, path: %s)",
		context, item.Code, item.Message,
		errDetails.APIName, errDetails.JSONPath,
	)
}

// extractRecordID unmarshals a success response item to get the created/updated record ID.
func extractRecordID(item entity.ZohoResponseItem) (string, error) {
	var success entity.SuccessOrderDetails
	if err := json.Unmarshal(item.Details, &success); err != nil {
		return "", fmt.Errorf("failed to parse record ID: %w", err)
	}
	return success.ID, nil
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
