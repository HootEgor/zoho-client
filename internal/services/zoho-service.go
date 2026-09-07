package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sync"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/httputil"
	"zohoclient/internal/lib/sl"
	"zohoclient/internal/lib/util"
)

// ErrPaymentInvalidData is returned by CreatePayment when Zoho rejects the
// payment with code INVALID_DATA — e.g. the linked Sales Order no longer exists.
// This is non-transient: retrying with the same data will always fail.
var ErrPaymentInvalidData = errors.New("payment invalid data")

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
	// tokenMu guards the three fields the OAuth refresh rewrites — the cached access token, its
	// expiry, and the API domain Zoho hands back with it. The poller, the HTTP push endpoint and
	// the status report all read them from different goroutines.
	tokenMu      sync.RWMutex
	refreshToken string
	initialToken string
	refreshUrl   string
	tokenExpiry  time.Time
	crmUrl       string
	scope        string
	apiVersion   string
	site         *config.SiteSettings
	log          *slog.Logger
	httpClient   *http.Client
}

func NewZohoService(conf *config.Config, site *config.SiteSettings, log *slog.Logger) (*ZohoService, error) {

	service := &ZohoService{
		clientID:     conf.Zoho.ClientId,
		clientSecret: conf.Zoho.ClientSecret,
		initialToken: conf.Zoho.RefreshToken,
		refreshUrl:   conf.Zoho.RefreshUrl,
		crmUrl:       conf.Zoho.CrmUrl,
		scope:        conf.Zoho.Scope,
		apiVersion:   conf.Zoho.ApiVersion,
		site:         site,
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
	if _, valid := s.TokenStatus(); valid {
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

	// Validate before publishing: a response without a token must leave the cached one alone
	// rather than replace it with an empty string the next request would send as a credential.
	if response.AccessToken == "" {
		s.log.With(slog.Any("response", response)).Debug("refresh token failed")
		return fmt.Errorf("empty access token")
	}

	s.tokenMu.Lock()
	s.refreshToken = response.AccessToken
	if response.ApiDomain != "" {
		s.crmUrl = response.ApiDomain
	}
	if response.ExpiresIn != 0 {
		s.tokenExpiry = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	s.tokenMu.Unlock()

	return nil
}

// TokenStatus reports when the cached access token expires and whether it is usable right now.
// A status report reads this instead of forcing a refresh, so asking for health never spends a
// Zoho API call.
func (s *ZohoService) TokenStatus() (expiry time.Time, valid bool) {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.tokenExpiry, s.refreshToken != "" && time.Now().Before(s.tokenExpiry)
}

// token returns the cached access token and the API domain to send it to, read together so a
// concurrent refresh cannot pair one request's token with another's domain.
func (s *ZohoService) token() (accessToken, crmUrl string) {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.refreshToken, s.crmUrl
}

// CreateContact creates or updates (upserts) a contact in the Zoho CRM Contacts module.
// Uses duplicate_check_fields to match on Email/Phone and return the existing record ID
// instead of failing with DUPLICATE_DATA.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/upsert-records.html
func (s *ZohoService) CreateContact(contact *entity.ClientDetails) (string, error) {
	return s.upsertClient(contact, true)
}

// UpsertContact pushes an OpenCart customer into the Zoho Contacts module without
// applying any placeholder defaults. Empty fields are omitted from the payload
// (see entity.Contact JSON tags) so that existing non-empty values in Zoho are
// preserved. Used by the customer sync loop.
func (s *ZohoService) UpsertContact(contact *entity.ClientDetails) (string, error) {
	return s.upsertClient(contact, false)
}

// upsertClient is the body shared by CreateContact and UpsertContact. The two differ only in
// namePlaceholders: an order needs a name on the Contact, so a missing one becomes "?", whereas
// the customer sync leaves it empty and lets omitempty preserve whatever Zoho already holds.
//
// The logger is built before the placeholders are applied, so the log reports the name OpenCart
// actually gave us.
func (s *ZohoService) upsertClient(contact *entity.ClientDetails, namePlaceholders bool) (string, error) {
	log := s.log.With(
		slog.String("email", contact.Email),
		slog.String("phone", contact.Phone),
		slog.String("name", fmt.Sprintf("%s : %s", contact.FirstName, contact.LastName)),
	)

	if err := util.ValidateEmail(contact.Email); err != nil {
		log.Debug("invalid email")
		contact.Email = ""
	}

	if contact.Email == "" && contact.Phone == "" {
		return "", fmt.Errorf("email and phone are empty")
	}

	if namePlaceholders {
		if contact.FirstName == "" {
			contact.FirstName = "?"
		}
		if contact.LastName == "" {
			contact.LastName = "?"
		}
	}

	payload := entity.Contact{
		Email:            contact.Email,
		Phone:            contact.Phone,
		FirstName:        contact.FirstName,
		LastName:         contact.LastName,
		City:             contact.City,
		Country:          contact.Country,
		CustomerCategory: s.site.CustomerCategory(contact.GroupId),
	}

	return s.upsertContact(payload, contactDuplicateCheckFields(payload), log)
}

// contactDuplicateCheckFields returns the subset of ["Email", "Phone"] that are
// populated on the given contact, so Zoho upsert can match an existing record
// instead of rejecting with DUPLICATE_DATA.
func contactDuplicateCheckFields(contact entity.Contact) []string {
	fields := []string{}
	if contact.Email != "" {
		fields = append(fields, "Email")
	}
	if contact.Phone != "" {
		fields = append(fields, "Phone")
	}
	return fields
}

// upsertContact sends the marshaled payload to Contacts/upsert and resolves the
// record ID, transparently extracting an existing ID from DUPLICATE_DATA and
// MULTIPLE_OR_MULTI_ERRORS responses.
func (s *ZohoService) upsertContact(contact entity.Contact, dupFields []string, log *slog.Logger) (string, error) {
	payload := records(contact)
	payload["duplicate_check_fields"] = dupFields

	item, err := s.writeRecord(http.MethodPost, payload, "Contacts", "upsert")
	if err != nil {
		return "", err
	}

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

	var successDetails entity.SuccessContactDetails
	if err = json.Unmarshal(item.Details, &successDetails); err != nil {
		return "", fmt.Errorf("failed to parse success ID: %w", err)
	}

	return successDetails.ID, nil
}

// CreateOrder creates a Sales Order in the Zoho CRM Sales_Orders module.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/insert-records.html
// Module: Sales_Orders - https://www.zoho.com/crm/developer/docs/api/v8/modules-api.html
func (s *ZohoService) CreateOrder(orderData entity.ZohoOrder) (string, string, error) {
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

	item, err := s.writeRecord(http.MethodPost, records(orderData), "Sales_Orders")
	if err != nil {
		return "", "", err
	}

	if item.Status != "success" {
		err = formatZohoError("order not created", item)
		return "", "", err
	}

	details, err := extractRecordDetails(item)
	if err != nil {
		return "", "", err
	}
	log = log.With(slog.String("id", details.ID))

	return details.ID, details.ModifiedTime, nil

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

	item, err := s.writeRecord(http.MethodPost, records(orderData), "Deals")
	if err != nil {
		return "", err
	}

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
	item, err := s.writeRecord(http.MethodPost, records(payment), "Payments")
	if err != nil {
		return "", err
	}

	if item.Status != "success" {
		err := formatZohoError("payment not created", item)
		if item.Code == "INVALID_DATA" {
			return "", fmt.Errorf("%w: %w", ErrPaymentInvalidData, err)
		}
		return "", err
	}

	return extractRecordID(item)
}

// UpdatePaymentStatus updates the Status field of an existing record in the custom
// "Payments" module, identified by its Zoho record id. Used to advance a payment
// (e.g. held -> paid) when wfsync reports a new Stripe payment status.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/update-specific-record.html
func (s *ZohoService) UpdatePaymentStatus(id, status string) error {
	item, err := s.writeRecord(http.MethodPut, records(map[string]any{"Status": status}), "Payments", id)
	if err != nil {
		return err
	}

	if item.Status != "success" {
		return formatZohoError("payment status not updated", item)
	}

	return nil
}

// AddItemsToOrderB2B creates records in the custom "Goods" module linked to a B2B Deal.
// Each Good references a Product and a Deal via lookup fields.
func (s *ZohoService) AddItemsToOrderB2B(_ string, items []*entity.Good) (string, error) {
	item, err := s.writeRecord(http.MethodPost, records(items...), "Goods")
	if err != nil {
		return "", err
	}

	if item.Status != "success" {
		return "", formatZohoError("items not added", item)
	}

	return extractRecordID(item)
}

// UpdateOrder updates an existing Sales Order record by its Zoho record ID, returning the
// record's new Modified_Time so the caller can suppress the echo webhook this write triggers.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/update-specific-record.html
func (s *ZohoService) UpdateOrder(orderData entity.ZohoOrder, id string) (string, error) {
	log := s.log.With(
		slog.String("id", id),
		slog.String("subject", orderData.Subject),
		slog.Float64("vat", orderData.VAT),
		slog.Float64("coupon", orderData.CouponValue),
		slog.Float64("sub_total", orderData.SubTotal),
		slog.Float64("total", orderData.GrandTotal),
	)

	item, err := s.writeRecord(http.MethodPut, records(orderData), "Sales_Orders", id)
	if err != nil {
		return "", err
	}

	if item.Status != "success" {
		err = formatZohoError("order not updated", item)
		log.With(sl.Err(err)).Error("order not updated")
		return "", err
	}

	details, err := extractRecordDetails(item)
	if err != nil {
		return "", err
	}

	log.Debug("order updated successfully")

	return details.ModifiedTime, nil
}

// GetOrder reads a Sales Order back from Zoho, including its Ordered_Items subform rows with
// the row ids needed to update them in place.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/get-records.html
func (s *ZohoService) GetOrder(orderID string) (*entity.ZohoOrderRecord, error) {
	body, err := s.doRawRequest(http.MethodGet, nil, "Sales_Orders", orderID)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []entity.ZohoOrderRecord `json:"data"`
	}
	if err = json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode order: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("order %s not found", orderID)
	}

	return &resp.Data[0], nil
}

// UpdateOrderItemRows updates existing Ordered_Items rows in place, matched by their subform row
// id. Rows not listed are left untouched — Zoho only removes a row when it is sent with
// "_delete": null, and only appends when a row arrives without an id.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/update-subforms.html
func (s *ZohoService) UpdateOrderItemRows(orderID string, rows []entity.OrderedItemPatch) (string, error) {
	if len(rows) == 0 {
		return "", fmt.Errorf("no rows to update")
	}
	for i, row := range rows {
		if row.ID == "" {
			return "", fmt.Errorf("row %d has no subform id: it would be appended as a new line", i)
		}
	}

	item, err := s.writeRecord(http.MethodPut, records(map[string]any{"Ordered_Items": rows}), "Sales_Orders", orderID)
	if err != nil {
		return "", err
	}

	if item.Status != "success" {
		return "", formatZohoError("order items not updated", item)
	}

	details, err := extractRecordDetails(item)
	if err != nil {
		return "", err
	}

	s.log.With(
		slog.String("id", orderID),
		slog.Int("rows", len(rows)),
	).Debug("order items updated")

	return details.ModifiedTime, nil
}

// records wraps values as the {"data": [...]} envelope every Zoho write takes.
func records[T any](items ...T) map[string]any {
	return map[string]any{"data": items}
}

// writeRecord marshals payload, sends it, and returns the single per-record result Zoho answers
// a write with. It deliberately stops there: which failures are meaningful differs by module
// (INVALID_DATA on Payments, DUPLICATE_DATA on Contacts), so each caller reads item.Status and
// phrases its own error.
func (s *ZohoService) writeRecord(method string, payload any, pathSegments ...string) (entity.ZohoResponseItem, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return entity.ZohoResponseItem{}, fmt.Errorf("marshal payload: %w", err)
	}

	apiResp, err := s.doRequest(method, body, pathSegments...)
	if err != nil {
		return entity.ZohoResponseItem{}, err
	}

	return apiResp.Data[0], nil
}

// send executes an authenticated request against the Zoho CRM v8 REST API: it refreshes the
// OAuth token, builds the full URL from path segments (e.g. "Sales_Orders", "upsert"), and
// returns the response body with its status code. Interpreting the status is left to the
// caller — a Zoho write reports a per-record failure in the body under a non-2xx status, and
// that body is the only place the error code lives.
// Ref: https://www.zoho.com/crm/developer/docs/api/v8/api-limits.html
func (s *ZohoService) send(method string, body []byte, pathSegments ...string) ([]byte, int, error) {
	if err := s.RefreshToken(); err != nil {
		return nil, 0, err
	}

	// Read the token and the API domain after the refresh: a refresh can move the domain, and the
	// URL must be built from the same snapshot the Authorization header comes from.
	accessToken, crmUrl := s.token()

	segments := append([]string{s.scope, s.apiVersion}, pathSegments...)
	fullURL, err := buildURL(crmUrl, segments...)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequest(method, fullURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("send request: %w", err)
	}
	defer httputil.CloseBody(resp.Body, s.log)

	// Check for rate limiting (v8 API has stricter limits)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, resp.StatusCode, fmt.Errorf("rate limited by Zoho API, retry after: %s", resp.Header.Get("Retry-After"))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return bodyBytes, resp.StatusCode, nil
}

// doRawRequest is doRequest for endpoints whose response is a record rather than the standard
// per-record status envelope. With no envelope to carry the reason, a non-2xx status is the
// error itself.
func (s *ZohoService) doRawRequest(method string, body []byte, pathSegments ...string) ([]byte, error) {
	bodyBytes, status, err := s.send(method, body, pathSegments...)
	if err != nil {
		return nil, err
	}

	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("zoho api: %d %s: %s", status, http.StatusText(status), string(bodyBytes))
	}

	return bodyBytes, nil
}

// doRequest sends a request and decodes the standard per-record status envelope. The HTTP status
// is not checked: Zoho answers a rejected write with 400 and puts the reason in the envelope, so
// failing on the status would discard the error code callers act on (see ErrPaymentInvalidData).
func (s *ZohoService) doRequest(method string, body []byte, pathSegments ...string) (*entity.ZohoAPIResponse, error) {
	bodyBytes, _, err := s.send(method, body, pathSegments...)
	if err != nil {
		return nil, err
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

// extractRecordDetails unmarshals a success response item into SuccessOrderDetails.
// Used when callers need fields beyond the record ID (e.g. Modified_Time for echo suppression).
func extractRecordDetails(item entity.ZohoResponseItem) (entity.SuccessOrderDetails, error) {
	var success entity.SuccessOrderDetails
	if err := json.Unmarshal(item.Details, &success); err != nil {
		return entity.SuccessOrderDetails{}, fmt.Errorf("failed to parse record details: %w", err)
	}
	return success, nil
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
