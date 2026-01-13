# Logistics Tracker Implementation Plan

## Overview

Implement a logistics tracking service that monitors Zoho CRM Deals, tracks shipments via DHL 24 API, and sends SMS notifications via TurboSMS when shipment status changes.

**Key Features:**
- Poll Zoho Deals every 10-30 minutes for shipments with TTN numbers
- Query DHL 24 tracking API for current shipment status
- Detect status changes and prevent duplicate notifications
- Send status-specific SMS messages in Ukrainian via TurboSMS
- Update Zoho CRM with latest status

---

## Architecture Integration

This service will run alongside the existing OpenCart-Zoho sync service:

```
┌─────────────────────────────────────────────────────────┐
│ main.go (cmd/zoho/main.go)                             │
├─────────────────────────────────────────────────────────┤
│ Goroutine 1: OpenCart Order Sync (existing)            │
│ Goroutine 2: Logistics Tracker (NEW)                   │
│ Goroutine 3: HTTP API Server (existing, commented out) │
└─────────────────────────────────────────────────────────┘

┌──────────────┐      ┌──────────────┐      ┌──────────────┐
│ Zoho CRM     │◄────►│ Core Logic   │◄────►│ DHL 24 API   │
│ (Deals)      │      │ (Tracker)    │      │ (SOAP)       │
└──────────────┘      └──────┬───────┘      └──────────────┘
                             │
                             ▼
                      ┌──────────────┐
                      │ TurboSMS API │
                      │ (REST)       │
                      └──────────────┘
```

---

## Phase 1: Project Structure & Configuration

### 1.1 New File Structure

```
entity/
  logistics.go                    # Data models for shipments, tracking

internal/
  services/
    dhl-service.go                # DHL 24 SOAP API client
    turbosms-service.go           # TurboSMS REST API client

impl/
  core/
    logistics.go                  # Main tracker orchestration
    logistics-processor.go        # Deal processing logic

config/
  statuses.yaml                   # Status code → SMS message mapping
```

### 1.2 Configuration Updates

**File:** `config.yml` (add new sections)

```yaml
# Logistics tracking configuration
logistics:
  enabled: true
  poll_interval: 15m              # How often to check shipments

  dhl24:
    api_url: "https://dhl24.com.pl/webapi2"
    username: "${DHL24_USERNAME}"
    password: "${DHL24_PASSWORD}"
    timeout: 30s

  turbosms:
    api_url: "https://api.turbosms.ua"
    auth_token: "${TURBOSMS_TOKEN}"
    sender: "YourBrand"           # Must be pre-registered
    timeout: 10s

  # Status mapping file location
  status_config: "config/statuses.yaml"

  # Zoho Deal filters
  zoho_filters:
    stage: "Shipped"              # Deal stage to monitor
    provider_field: "Logistics_Provider"
    provider_value: "DHL24"
```

**File:** `config/statuses.yaml` (new file)

```yaml
# DHL 24 status codes → SMS messages (Ukrainian)
# Based on DHL tracking statuses
statuses:
  PICKED_UP:
    sms: "📦 Шановний клієнте! Ваше відправлення прийнято DHL. Номер ТТН: {ttn}"

  IN_TRANSIT:
    sms: "🚚 Відправлення {ttn} в дорозі. Очікуйте оновлення."

  ARRIVED_AT_DEPOT:
    sms: "🏬 Відправлення {ttn} прибуло у відділення DHL."

  OUT_FOR_DELIVERY:
    sms: "📍 Кур'єр прямує до вас! Відправлення {ttn} буде доставлено сьогодні."

  DELIVERED:
    sms: "✅ Відправлення {ttn} успішно доставлено. Дякуємо за покупку!"

  DELIVERY_FAILED:
    sms: "❌ Не вдалося доставити {ttn}. Будь ласка, зв'яжіться з нами."

  RETURNED:
    sms: "🔙 Відправлення {ttn} повертається відправнику."

  EXCEPTION:
    sms: "⚠️ Затримка відправлення {ttn}. Уточнюйте деталі у службі підтримки."
```

### 1.3 Environment Variables

**Add to deployment scripts:**

```bash
# DHL 24 API credentials
DHL24_USERNAME=your_api_username
DHL24_PASSWORD=your_api_password

# TurboSMS credentials
TURBOSMS_TOKEN=your_bearer_token
```

---

## Phase 2: Data Models

### 2.1 Zoho CRM Schema Changes

**Required Custom Fields in Zoho Deals module:**

| Field Name | API Name | Type | Purpose |
|------------|----------|------|---------|
| TTN Number | `TTN_Number` | Text | DHL tracking number |
| Shipment Status | `Shipment_Status` | Picklist | Current DHL status |
| Last Notified Status | `Last_Notified_Status` | Text | Prevents duplicate SMS |
| Customer Phone | `Phone` | Phone | SMS recipient |
| Logistics Provider | `Logistics_Provider` | Picklist | DHL24/Other |
| Last Status Check | `Last_Status_Check` | DateTime | API polling timestamp |

**Zoho Setup Steps:**
1. Go to Zoho CRM → Settings → Customization → Modules → Deals
2. Add custom fields as listed above
3. Add picklist values for `Logistics_Provider`: DHL24, NovaPost, UkrPost, etc.
4. Add picklist values for `Shipment_Status`: (populated from DHL responses)

### 2.2 Entity Models

**File:** `entity/logistics.go`

```go
package entity

import "time"

// ZohoDeal represents a Zoho CRM Deal with shipment tracking fields
type ZohoDeal struct {
    ID                   string    `json:"id"`
    DealName             string    `json:"Deal_Name"`
    TTNNumber            string    `json:"TTN_Number"`
    ShipmentStatus       string    `json:"Shipment_Status"`
    LastNotifiedStatus   string    `json:"Last_Notified_Status"`
    Phone                string    `json:"Phone"`
    LogisticsProvider    string    `json:"Logistics_Provider"`
    LastStatusCheck      time.Time `json:"Last_Status_Check"`
    Stage                string    `json:"Stage"`
}

// DHLTrackingResponse represents DHL tracking API response
type DHLTrackingResponse struct {
    TrackingNumber string                 `xml:"trackingNumber"`
    Events         []DHLTrackingEvent     `xml:"events>event"`
    CurrentStatus  string                 `xml:"currentStatus"`
}

// DHLTrackingEvent represents individual scan event
type DHLTrackingEvent struct {
    Timestamp   time.Time `xml:"timestamp"`
    StatusCode  string    `xml:"statusCode"`
    StatusText  string    `xml:"statusText"`
    Location    string    `xml:"location"`
}

// TurboSMSRequest represents TurboSMS API request
type TurboSMSRequest struct {
    Recipients []string         `json:"recipients"`
    SMS        TurboSMSMessage  `json:"sms"`
}

// TurboSMSMessage represents SMS content
type TurboSMSMessage struct {
    Sender string `json:"sender"`
    Text   string `json:"text"`
}

// TurboSMSResponse represents TurboSMS API response
type TurboSMSResponse struct {
    ResponseCode   int         `json:"response_code"`
    ResponseStatus string      `json:"response_status"`
    ResponseResult interface{} `json:"response_result"`
}

// StatusMapping represents status → SMS message mapping
type StatusMapping struct {
    SMS string `yaml:"sms"`
}

// StatusConfig represents the entire status configuration
type StatusConfig struct {
    Statuses map[string]StatusMapping `yaml:"statuses"`
}
```

---

## Phase 3: External Service Clients

### 3.1 DHL 24 SOAP Client

**File:** `internal/services/dhl-service.go`

**Dependencies:**
```bash
go get github.com/hooklift/gowsdl/soap
```

**Implementation outline:**

```go
package services

import (
    "context"
    "log/slog"
    "time"

    "github.com/hooklift/gowsdl/soap"
    "zohoclient/entity"
)

type DHLService struct {
    client   *soap.Client
    username string
    password string
    log      *slog.Logger
}

func NewDHLService(apiURL, username, password string, timeout time.Duration, log *slog.Logger) *DHLService {
    client := soap.NewClient(apiURL, soap.WithTimeout(timeout))
    return &DHLService{
        client:   client,
        username: username,
        password: password,
        log:      log,
    }
}

// GetTrackingInfo calls DHL getTrackAndTraceInfo SOAP method
func (s *DHLService) GetTrackingInfo(ctx context.Context, ttn string) (*entity.DHLTrackingResponse, error) {
    // Build SOAP request with AuthData + tracking number
    // Call s.client.Call(ctx, "getTrackAndTraceInfo", request, &response)
    // Parse response into entity.DHLTrackingResponse
    // Return normalized status code
}

// normalizeStatus converts DHL status codes to our internal status codes
func (s *DHLService) normalizeStatus(dhlStatus string) string {
    // Map DHL-specific codes to our standardized codes
    // Examples: "DD" → "DELIVERED", "PU" → "PICKED_UP", etc.
}
```

**Key implementation notes:**
- Use `hooklift/gowsdl` library for SOAP calls
- Generate Go types from DHL WSDL: `gowsdl -o internal/services/dhl -p dhl https://dhl24.com.pl/webapi2`
- Handle SOAP faults and network errors gracefully
- Add retry logic with exponential backoff (3 attempts)
- Log all API calls with structured logging

### 3.2 TurboSMS REST Client

**File:** `internal/services/turbosms-service.go`

```go
package services

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "time"

    "zohoclient/entity"
)

type TurboSMSService struct {
    baseURL   string
    authToken string
    sender    string
    client    *http.Client
    log       *slog.Logger
}

func NewTurboSMSService(baseURL, authToken, sender string, timeout time.Duration, log *slog.Logger) *TurboSMSService {
    return &TurboSMSService{
        baseURL:   baseURL,
        authToken: authToken,
        sender:    sender,
        client:    &http.Client{Timeout: timeout},
        log:       log,
    }
}

// SendSMS sends SMS via TurboSMS API
func (s *TurboSMSService) SendSMS(ctx context.Context, phone, message string) error {
    const op = "services.TurboSMSService.SendSMS"
    log := s.log.With(slog.String("op", op))

    // Build request
    req := entity.TurboSMSRequest{
        Recipients: []string{phone},
        SMS: entity.TurboSMSMessage{
            Sender: s.sender,
            Text:   message,
        },
    }

    body, err := json.Marshal(req)
    if err != nil {
        return fmt.Errorf("%s: marshal request: %w", op, err)
    }

    // Create HTTP request
    httpReq, err := http.NewRequestWithContext(ctx, "POST",
        s.baseURL+"/message/send.json", bytes.NewReader(body))
    if err != nil {
        return fmt.Errorf("%s: create request: %w", op, err)
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+s.authToken)

    // Execute request
    resp, err := s.client.Do(httpReq)
    if err != nil {
        return fmt.Errorf("%s: execute request: %w", op, err)
    }
    defer resp.Body.Close()

    // Parse response
    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("%s: read response: %w", op, err)
    }

    var smsResp entity.TurboSMSResponse
    if err := json.Unmarshal(respBody, &smsResp); err != nil {
        return fmt.Errorf("%s: unmarshal response: %w", op, err)
    }

    // Check response code
    if smsResp.ResponseCode != 0 {
        return fmt.Errorf("%s: API error %d: %s", op, smsResp.ResponseCode, smsResp.ResponseStatus)
    }

    log.Info("SMS sent successfully",
        slog.String("phone", phone),
        slog.Int("response_code", smsResp.ResponseCode))

    return nil
}

// normalizePhone converts phone to international format (380XXXXXXXXX)
func (s *TurboSMSService) normalizePhone(phone string) string {
    // Remove spaces, dashes, parentheses
    // Add country code if missing (380 for Ukraine)
    // Validate format
}
```

**Key implementation notes:**
- Use Bearer token authentication (most reliable)
- Validate phone numbers before sending
- Handle TurboSMS error codes (203=insufficient balance, 305=invalid phone, etc.)
- Add retry logic for transient failures
- Rate limit: max 5000 recipients per request (not applicable for single SMS)
- Log successful sends and failures

---

## Phase 4: Core Business Logic

### 4.1 Main Tracker Orchestrator

**File:** `impl/core/logistics.go`

```go
package core

import (
    "context"
    "log/slog"
    "time"

    "zohoclient/entity"
    "zohoclient/internal/services"
)

type LogisticsTracker struct {
    zoho          *services.ZohoService
    dhl           *services.DHLService
    sms           *services.TurboSMSService
    statusConfig  *entity.StatusConfig
    pollInterval  time.Duration
    filters       LogisticsFilters
    log           *slog.Logger
}

type LogisticsFilters struct {
    Stage         string
    ProviderField string
    ProviderValue string
}

func NewLogisticsTracker(
    zoho *services.ZohoService,
    dhl *services.DHLService,
    sms *services.TurboSMSService,
    statusConfig *entity.StatusConfig,
    pollInterval time.Duration,
    filters LogisticsFilters,
    log *slog.Logger,
) *LogisticsTracker {
    return &LogisticsTracker{
        zoho:         zoho,
        dhl:          dhl,
        sms:          sms,
        statusConfig: statusConfig,
        pollInterval: pollInterval,
        filters:      filters,
        log:          log,
    }
}

// Start begins the logistics tracking loop (runs in goroutine)
func (t *LogisticsTracker) Start(ctx context.Context) error {
    const op = "core.LogisticsTracker.Start"
    log := t.log.With(slog.String("op", op))

    log.Info("Starting logistics tracker",
        slog.Duration("poll_interval", t.pollInterval))

    ticker := time.NewTicker(t.pollInterval)
    defer ticker.Stop()

    // Run immediately on start
    if err := t.ProcessDeals(ctx); err != nil {
        log.Error("Initial processing failed", slog.String("error", err.Error()))
    }

    for {
        select {
        case <-ctx.Done():
            log.Info("Logistics tracker stopped")
            return ctx.Err()

        case <-ticker.C:
            if err := t.ProcessDeals(ctx); err != nil {
                log.Error("Processing failed", slog.String("error", err.Error()))
            }
        }
    }
}

// ProcessDeals is the main processing loop
func (t *LogisticsTracker) ProcessDeals(ctx context.Context) error {
    const op = "core.LogisticsTracker.ProcessDeals"
    log := t.log.With(slog.String("op", op))

    // 1. Fetch deals from Zoho
    deals, err := t.fetchDeals(ctx)
    if err != nil {
        return fmt.Errorf("%s: fetch deals: %w", op, err)
    }

    log.Info("Fetched deals for processing", slog.Int("count", len(deals)))

    // 2. Process each deal
    successCount := 0
    errorCount := 0

    for _, deal := range deals {
        if err := t.processDeal(ctx, deal); err != nil {
            log.Error("Failed to process deal",
                slog.String("deal_id", deal.ID),
                slog.String("ttn", deal.TTNNumber),
                slog.String("error", err.Error()))
            errorCount++
        } else {
            successCount++
        }
    }

    log.Info("Processing completed",
        slog.Int("success", successCount),
        slog.Int("errors", errorCount))

    return nil
}
```

### 4.2 Deal Processor

**File:** `impl/core/logistics-processor.go`

```go
package core

import (
    "context"
    "fmt"
    "log/slog"
    "strings"

    "zohoclient/entity"
    "zohoclient/internal/lib/logger/sl"
)

// fetchDeals retrieves deals from Zoho CRM matching filters
func (t *LogisticsTracker) fetchDeals(ctx context.Context) ([]entity.ZohoDeal, error) {
    const op = "core.LogisticsTracker.fetchDeals"

    // Build Zoho search criteria
    // Format: ((Stage:equals:Shipped)and(Logistics_Provider:equals:DHL24)and(TTN_Number:is not empty))
    criteria := fmt.Sprintf(
        "((%s:equals:%s)and(%s:equals:%s)and(TTN_Number:is not empty))",
        "Stage", t.filters.Stage,
        t.filters.ProviderField, t.filters.ProviderValue,
    )

    // Call Zoho API
    deals, err := t.zoho.SearchDeals(ctx, criteria)
    if err != nil {
        return nil, fmt.Errorf("%s: search deals: %w", op, err)
    }

    return deals, nil
}

// processDeal handles a single deal
func (t *LogisticsTracker) processDeal(ctx context.Context, deal entity.ZohoDeal) error {
    const op = "core.LogisticsTracker.processDeal"
    log := t.log.With(
        slog.String("op", op),
        slog.String("deal_id", deal.ID),
        slog.String("ttn", deal.TTNNumber),
    )

    // 1. Validate required fields
    if deal.TTNNumber == "" {
        return fmt.Errorf("%s: missing TTN number", op)
    }
    if deal.Phone == "" {
        log.Warn("Deal has no phone number, skipping SMS")
    }

    // 2. Get tracking info from DHL
    trackingInfo, err := t.dhl.GetTrackingInfo(ctx, deal.TTNNumber)
    if err != nil {
        return fmt.Errorf("%s: get tracking info: %w", op, err)
    }

    currentStatus := trackingInfo.CurrentStatus
    log.Info("Fetched DHL status", slog.String("status", currentStatus))

    // 3. Check if status changed
    if currentStatus == deal.ShipmentStatus {
        log.Debug("Status unchanged, skipping")
        return t.updateLastCheck(ctx, deal.ID)
    }

    // 4. Check if we already notified about this status
    if currentStatus == deal.LastNotifiedStatus {
        log.Info("Already notified about this status, updating Zoho only")
        return t.updateDealStatus(ctx, deal.ID, currentStatus, false)
    }

    // 5. Send SMS notification
    if deal.Phone != "" {
        if err := t.sendNotification(ctx, deal, currentStatus); err != nil {
            // Log error but don't fail the entire process
            log.Error("Failed to send SMS", sl.Err(err))
            // Update Zoho without marking as notified
            return t.updateDealStatus(ctx, deal.ID, currentStatus, false)
        }
    }

    // 6. Update Zoho with new status + notification flag
    return t.updateDealStatus(ctx, deal.ID, currentStatus, true)
}

// sendNotification sends SMS via TurboSMS
func (t *LogisticsTracker) sendNotification(ctx context.Context, deal entity.ZohoDeal, status string) error {
    const op = "core.LogisticsTracker.sendNotification"

    // Get message template
    statusMapping, exists := t.statusConfig.Statuses[status]
    if !exists {
        return fmt.Errorf("%s: no SMS template for status %s", op, status)
    }

    // Replace placeholders
    message := strings.ReplaceAll(statusMapping.SMS, "{ttn}", deal.TTNNumber)
    message = strings.ReplaceAll(message, "{deal}", deal.DealName)

    // Send SMS
    return t.sms.SendSMS(ctx, deal.Phone, message)
}

// updateDealStatus updates Zoho deal with new status
func (t *LogisticsTracker) updateDealStatus(ctx context.Context, dealID, status string, notified bool) error {
    const op = "core.LogisticsTracker.updateDealStatus"

    updateData := map[string]interface{}{
        "Shipment_Status":     status,
        "Last_Status_Check":   time.Now().Format(time.RFC3339),
    }

    if notified {
        updateData["Last_Notified_Status"] = status
    }

    return t.zoho.UpdateDeal(ctx, dealID, updateData)
}

// updateLastCheck updates only the Last_Status_Check timestamp
func (t *LogisticsTracker) updateLastCheck(ctx context.Context, dealID string) error {
    updateData := map[string]interface{}{
        "Last_Status_Check": time.Now().Format(time.RFC3339),
    }
    return t.zoho.UpdateDeal(ctx, dealID, updateData)
}
```

---

## Phase 5: Zoho Service Extensions

**File:** `internal/services/zoho-service.go` (add methods)

```go
// SearchDeals searches for deals matching criteria
func (s *ZohoService) SearchDeals(ctx context.Context, criteria string) ([]entity.ZohoDeal, error) {
    const op = "services.ZohoService.SearchDeals"

    // Refresh token
    if err := s.RefreshToken(); err != nil {
        return nil, fmt.Errorf("%s: refresh token: %w", op, err)
    }

    // Build URL: GET /crm/v2/Deals/search?criteria=(...)
    url := s.buildURL("/crm/v2/Deals/search")
    url += "?criteria=" + criteria

    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("%s: create request: %w", op, err)
    }

    req.Header.Set("Authorization", "Zoho-oauthtoken "+s.accessToken)

    resp, err := s.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("%s: execute request: %w", op, err)
    }
    defer resp.Body.Close()

    // Parse response
    var result struct {
        Data []entity.ZohoDeal `json:"data"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("%s: decode response: %w", op, err)
    }

    return result.Data, nil
}

// UpdateDeal updates a Zoho deal
func (s *ZohoService) UpdateDeal(ctx context.Context, dealID string, data map[string]interface{}) error {
    const op = "services.ZohoService.UpdateDeal"

    // Refresh token
    if err := s.RefreshToken(); err != nil {
        return fmt.Errorf("%s: refresh token: %w", op, err)
    }

    // Build URL: PUT /crm/v2/Deals/{id}
    url := s.buildURL(fmt.Sprintf("/crm/v2/Deals/%s", dealID))

    // Build request body
    body := map[string]interface{}{
        "data": []map[string]interface{}{data},
    }

    bodyJSON, err := json.Marshal(body)
    if err != nil {
        return fmt.Errorf("%s: marshal body: %w", op, err)
    }

    req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(bodyJSON))
    if err != nil {
        return fmt.Errorf("%s: create request: %w", op, err)
    }

    req.Header.Set("Authorization", "Zoho-oauthtoken "+s.accessToken)
    req.Header.Set("Content-Type", "application/json")

    resp, err := s.client.Do(req)
    if err != nil {
        return fmt.Errorf("%s: execute request: %w", op, err)
    }
    defer resp.Body.Close()

    // Check response
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("%s: unexpected status %d", op, resp.StatusCode)
    }

    return nil
}
```

---

## Phase 6: Main Application Integration

**File:** `cmd/zoho/main.go` (modifications)

```go
func main() {
    // ... existing config loading ...

    // Load status configuration
    statusConfig, err := loadStatusConfig(cfg.Logistics.StatusConfig)
    if err != nil {
        log.Error("failed to load status config", sl.Err(err))
        os.Exit(1)
    }

    // Initialize DHL service
    dhlService := services.NewDHLService(
        cfg.Logistics.DHL24.APIURL,
        cfg.Logistics.DHL24.Username,
        cfg.Logistics.DHL24.Password,
        cfg.Logistics.DHL24.Timeout,
        log,
    )

    // Initialize TurboSMS service
    smsService := services.NewTurboSMSService(
        cfg.Logistics.TurboSMS.APIURL,
        cfg.Logistics.TurboSMS.AuthToken,
        cfg.Logistics.TurboSMS.Sender,
        cfg.Logistics.TurboSMS.Timeout,
        log,
    )

    // Initialize logistics tracker
    tracker := core.NewLogisticsTracker(
        zohoService,
        dhlService,
        smsService,
        statusConfig,
        cfg.Logistics.PollInterval,
        core.LogisticsFilters{
            Stage:         cfg.Logistics.ZohoFilters.Stage,
            ProviderField: cfg.Logistics.ZohoFilters.ProviderField,
            ProviderValue: cfg.Logistics.ZohoFilters.ProviderValue,
        },
        log,
    )

    // Create context with cancellation
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Start logistics tracker in goroutine (if enabled)
    if cfg.Logistics.Enabled {
        go func() {
            if err := tracker.Start(ctx); err != nil {
                log.Error("logistics tracker stopped", sl.Err(err))
            }
        }()
    }

    // ... existing order processing logic ...
    // Continue with existing infinite loop or add graceful shutdown
}

// loadStatusConfig loads status mapping from YAML
func loadStatusConfig(path string) (*entity.StatusConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config file: %w", err)
    }

    var config entity.StatusConfig
    if err := yaml.Unmarshal(data, &config); err != nil {
        return nil, fmt.Errorf("unmarshal config: %w", err)
    }

    return &config, nil
}
```

---

## Phase 7: Error Handling & Resilience

### 7.1 Error Scenarios & Handling

| Scenario | Handling Strategy |
|----------|------------------|
| DHL API timeout | Retry 3 times with exponential backoff, log error, continue to next deal |
| DHL API returns unknown status | Log warning, skip SMS, update Zoho with raw status |
| TurboSMS API fails | Log error, update Zoho status but NOT Last_Notified_Status (will retry next cycle) |
| Invalid phone number | Log warning, update Zoho status without sending SMS |
| Zoho API rate limit (429) | Sleep for 60s, retry (implement in RefreshToken logic) |
| Missing TTN | Skip deal, log warning |
| Network connection lost | Retry on next poll cycle (10-30 min), log error |
| SOAP parsing error | Log error with full response, skip deal |

### 7.2 Retry Logic Pattern

```go
func retryWithBackoff(ctx context.Context, maxAttempts int, fn func() error) error {
    backoff := time.Second

    for attempt := 1; attempt <= maxAttempts; attempt++ {
        if err := fn(); err != nil {
            if attempt == maxAttempts {
                return fmt.Errorf("max retries exceeded: %w", err)
            }

            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-time.After(backoff):
                backoff *= 2 // exponential backoff
            }
            continue
        }
        return nil
    }
    return nil
}
```

### 7.3 Logging Strategy

- Use structured logging (slog) with consistent fields:
  - `op`: operation name
  - `deal_id`: Zoho deal ID
  - `ttn`: tracking number
  - `status`: current shipment status
  - `error`: error message

- Log levels:
  - **DEBUG**: Status unchanged, API responses
  - **INFO**: Processing started/completed, SMS sent, status updated
  - **WARN**: Missing phone, unknown status code, validation errors
  - **ERROR**: API failures, retry exhausted, critical errors

---

## Phase 8: Testing Strategy

### 8.1 Unit Tests

**File:** `impl/core/logistics_test.go`

```go
package core_test

import (
    "testing"
    "context"

    "zohoclient/entity"
    "zohoclient/impl/core"
)

// Test status change detection
func TestStatusChangeDetection(t *testing.T) {
    // Mock services
    // Create tracker
    // Test cases:
    // 1. Status unchanged → no SMS
    // 2. Status changed → SMS sent
    // 3. Already notified → Zoho update only
}

// Test phone number normalization
func TestPhoneNormalization(t *testing.T) {
    // Test cases:
    // 1. +380671234567 → 380671234567
    // 2. 0671234567 → 380671234567
    // 3. Invalid format → error
}

// Test message template replacement
func TestMessageTemplateReplacement(t *testing.T) {
    // Verify {ttn} and {deal} placeholders replaced correctly
}
```

### 8.2 Integration Tests

**Manual testing checklist:**

1. **DHL API connectivity**
   - [ ] Valid TTN returns tracking info
   - [ ] Invalid TTN returns error
   - [ ] SOAP authentication works

2. **TurboSMS API connectivity**
   - [ ] SMS sent successfully (check phone)
   - [ ] Invalid phone returns error
   - [ ] Insufficient balance error handled

3. **Zoho integration**
   - [ ] Deals fetched correctly
   - [ ] Status updated in Zoho
   - [ ] Last_Notified_Status prevents duplicates

4. **End-to-end flow**
   - [ ] Create test deal in Zoho with TTN
   - [ ] Wait for poll cycle
   - [ ] Verify SMS received
   - [ ] Verify Zoho updated
   - [ ] Change status manually in DHL
   - [ ] Verify second SMS received

### 8.3 Load Testing

- Simulate 100+ deals with TTNs
- Measure API call latency
- Check memory/CPU usage
- Verify no goroutine leaks

---

## Phase 9: Deployment

### 9.1 Build Process

**Update GitHub Actions:** `.github/workflows/deploy.yml`

```yaml
- name: Build application
  run: |
    go mod download
    go build -v -o zohoclient ./cmd/zoho

- name: Copy configuration files
  run: |
    mkdir -p /etc/conf
    envsubst < zohoclient-config.yml > /etc/conf/config.yml
    cp config/statuses.yaml /etc/conf/statuses.yaml
```

### 9.2 Systemd Service

**Update:** `/etc/systemd/system/zohoclient.service`

```ini
[Unit]
Description=Zoho Integration Service
After=network.target

[Service]
Type=simple
User=zohoclient
WorkingDirectory=/usr/local/bin
ExecStart=/usr/local/bin/zohoclient -conf=/etc/conf/config.yml -log=/var/log/zohoclient/
Restart=always
RestartSec=10

# Environment variables
Environment="DHL24_USERNAME=your_username"
Environment="DHL24_PASSWORD=your_password"
Environment="TURBOSMS_TOKEN=your_token"

[Install]
WantedBy=multi-user.target
```

### 9.3 Monitoring

**Add health check endpoint (future enhancement):**

```go
// GET /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
    status := map[string]string{
        "status":           "healthy",
        "logistics_enabled": strconv.FormatBool(cfg.Logistics.Enabled),
        "last_poll":        lastPollTime.Format(time.RFC3339),
    }
    json.NewEncoder(w).Encode(status)
}
```

**Logs to monitor:**
- `/var/log/zohoclient/app.log` - application logs
- Check for ERROR level entries
- Monitor SMS send success rate

---

## Phase 10: Implementation Timeline

### Week 1: Foundation
- [ ] Add configuration structure (config.yml, statuses.yaml)
- [ ] Create entity models (logistics.go)
- [ ] Set up Zoho custom fields
- [ ] Generate DHL WSDL client code

### Week 2: External Services
- [ ] Implement DHL SOAP client with retry logic
- [ ] Implement TurboSMS REST client
- [ ] Add Zoho service methods (SearchDeals, UpdateDeal)
- [ ] Unit test all service clients

### Week 3: Core Logic
- [ ] Implement LogisticsTracker orchestrator
- [ ] Implement deal processor with status detection
- [ ] Add message templating and SMS sending
- [ ] Integration testing with mock services

### Week 4: Integration & Testing
- [ ] Integrate into main.go
- [ ] End-to-end testing with real APIs (sandbox)
- [ ] Load testing with 100+ deals
- [ ] Fix bugs and refine error handling

### Week 5: Deployment & Monitoring
- [ ] Update deployment scripts
- [ ] Deploy to staging environment
- [ ] Production deployment
- [ ] Monitor for 7 days, iterate

---

## Success Criteria

1. **Functional Requirements**
   - [x] Service polls Zoho every 15 minutes
   - [x] Fetches only deals with Stage=Shipped, Provider=DHL24, TTN populated
   - [x] Queries DHL API for each TTN
   - [x] Detects status changes accurately
   - [x] Sends SMS only when status changes
   - [x] Prevents duplicate SMS via Last_Notified_Status
   - [x] Updates Zoho with latest status

2. **Performance Requirements**
   - [x] Processes 100 deals in <2 minutes
   - [x] DHL API calls complete in <30s each
   - [x] SMS sent in <5s
   - [x] Memory usage <100MB

3. **Reliability Requirements**
   - [x] Handles API failures gracefully
   - [x] Retries transient errors
   - [x] Logs all errors for debugging
   - [x] No data loss on service restart

---

## Future Enhancements

1. **Multi-provider support**
   - Add NovaPost, UkrPost, Meest APIs
   - Provider-specific status normalization

2. **Advanced notifications**
   - Support Viber messages via TurboSMS
   - Template customization per customer
   - Multi-language support

3. **Analytics dashboard**
   - Delivery time metrics
   - SMS delivery success rate
   - Status distribution charts

4. **Webhook integration**
   - Receive real-time updates from DHL (if available)
   - Reduce polling frequency

5. **Customer self-service**
   - Generate tracking page links
   - Send tracking URL via SMS

---

## References

- DHL 24 API Documentation: https://dhl24.com.pl/webapi2/doc/index.html
- TurboSMS API Documentation: https://turbosms.ua/ua/api.html
- Zoho CRM API: https://www.zoho.com/crm/developer/docs/api/v2/
- Go SOAP Client: https://github.com/hooklift/gowsdl
