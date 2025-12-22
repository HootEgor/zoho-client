# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based integration service that synchronizes OpenCart orders with Zoho CRM. It monitors an OpenCart MySQL database for new orders and creates corresponding contacts and sales orders in Zoho CRM via their REST API. The service includes optional Telegram bot notifications for monitoring.

**Key workflow:**
1. Polls OpenCart database every 2 minutes for orders with specific statuses
2. Creates/finds contacts in Zoho CRM
3. Fetches product Zoho IDs from an external product repository
4. Creates sales orders in Zoho CRM with line items (chunked for large orders)
5. Updates OpenCart orders with Zoho IDs to prevent duplicate processing

## Build and Run

### Building
```bash
# Build the application
go build -v -o zohoclient ./cmd/zoho

# Build for production
GOOS=linux GOARCH=amd64 go build -o zohoclient ./cmd/zoho
```

### Running
```bash
# Run with default config (config.yml in current directory)
./zohoclient

# Run with custom config and log path
./zohoclient -conf=/etc/conf/config.yml -log=/var/log/

# Run directly with go
go run ./cmd/zoho -conf=config.yml
```

### Testing
```bash
# Run all tests
go test ./...

# Run tests for a specific package
go test ./internal/lib/api/request/
go test ./internal/lib/api/response/

# Run tests with verbose output
go test -v ./...
```

Note: Most testing is done via integration testing against live databases and APIs. Unit tests exist only for the HTTP API layer (request/response utilities).

## Configuration

Configuration is managed via YAML files. The template is `config.yml` but the production config is `zohoclient-config.yml` (which uses environment variable placeholders for CI/CD).

**Key configuration sections:**
- `env`: Environment name for logging (local, production, etc.)
- `sql`: OpenCart database connection (can be disabled with `enabled: false`)
- `telegram`: Optional Telegram bot for admin notifications
- `zoho`: Zoho CRM API credentials (OAuth refresh token flow)
- `prod_repo`: External product repository API for fetching Zoho product IDs
- `listen`: HTTP API server settings (bind IP, port, authentication key)

See `docs/config.md` for detailed configuration structure.

## Code Architecture

### Directory Structure

```
cmd/zoho/                   # Application entry point (main.go)
entity/                     # Data models (orders, contacts, products, API responses)
internal/
  config/                   # Configuration loading (cleanenv)
  database/                 # OpenCart MySQL client and queries
  http-server/             # HTTP REST API server
    api/                    # Router setup and server initialization
    handlers/               # HTTP request handlers (order updates, errors)
    middleware/             # Authentication, timeout middleware
  lib/                      # Utilities (logger, validation, email, clock, API helpers)
  services/                 # External service clients (Zoho API, Product Repo)
impl/
  core/                     # Business logic orchestration (order processing, API auth)
  telegram/                 # Legacy Telegram implementation (deprecated)
bot/                        # Active Telegram bot implementation
docs/                       # API documentation (apiv1.md, config.md)
```

### Core Components

**impl/core/Core**
- Central orchestrator that coordinates between database, Zoho, and product repository
- Runs order processing loop every 2 minutes via `Start()` goroutine
- Main entry point: `ProcessOrders()` in `impl/core/orders.go:19`

**internal/database/MySql**
- OpenCart database client with prepared statements
- Automatically adds `zoho_id` columns to `product` and `order` tables if missing
- Retrieves orders by status, processes line items, totals, and custom fields
- Handles OpenCart's quirky tax calculation logic (see `OrderProducts()` around line 336)

**internal/services/ZohoService**
- Manages OAuth token refresh (stores token in memory with expiry tracking)
- Creates contacts with duplicate detection handling (`DUPLICATE_DATA` errors)
- Creates sales orders with subform item chunking (max 100 items per API call)
- Updates orders by adding items via bulk update API

**internal/services/ProductRepo**
- Fetches product Zoho IDs from external REST API using Basic Auth
- Used when OpenCart products don't have `zoho_id` populated yet

**bot/TgBot**
- Telegram bot for admin notifications (uses PaulSonOfLars/gotgbot library)
- Supports per-admin log level filtering via `/level` command
- Logger handler sends formatted messages to admins based on log levels

**internal/http-server/api/Server**
- HTTP REST API server built with chi router (currently stub implementation)
- Listens on configured bind IP and port from `listen` config section
- Protected by Bearer token authentication middleware
- Provides `/api/v1/order` endpoint for order updates (not yet implemented)
- See `docs/apiv1.md` for API documentation

### Data Flow

1. **Order Retrieval** (`database.GetNewOrders()`)
   - Fetches orders with statuses: New (1), Payed (2), PrepareForShipping (15)
   - Only processes orders from last 30 days
   - Excludes B2B orders (identified by customer group ID)

2. **Validation** (`impl/core/orders.go:85-103`)
   - Checks for empty product UIDs (fails fast)
   - Attempts to fetch missing Zoho IDs from product repository
   - Fails if Zoho IDs still missing after fetch attempt

3. **Order Building** (`buildZohoOrder()`)
   - Converts OpenCart money values (stored as cents) to floats
   - Chunks line items if >100 items (Zoho API limitation)
   - Adds metadata: location="Польша", source="OpenCart"

4. **Zoho Sync** (`impl/core/orders.go:107-142`)
   - Creates contact (handles duplicates gracefully)
   - Creates order with initial items
   - Adds remaining items via chunked updates
   - Updates OpenCart order with Zoho ID or "[B2B]" marker

### Important Details

**Money Handling**
- OpenCart stores prices in cents (int64)
- Conversion: `float64(value) / 100.0` (see `roundInt()`)
- Currency conversion applied: `value * currencyValue`

**Tax Calculation**
- OpenCart module 'OrderPRO' has defective tax logic
- Code detects variants by checking if `tax/price > 0.25` (see `OrderProducts()` around line 340)
- Falls back to standard OpenCart logic if ratio is normal

**Zoho API Error Handling**
- Gracefully handles `DUPLICATE_DATA` errors by extracting existing contact ID
- Also handles `MULTIPLE_OR_MULTI_ERRORS` with embedded duplicate info
- Token refresh happens automatically before each API call with 3 retry attempts

**B2B Orders**
- Identified by `customer_group_id` via `ClientDetails.IsB2B()`
- Skipped from Zoho sync but marked with `zoho_id = "[B2B]"`

**Database Schema Modifications**
- Application automatically adds `zoho_id VARCHAR(64)` columns to OpenCart tables
- Uses prepared statements stored in `statements` map for performance
- Connection pooling: 50 max open, 10 max idle, 1-hour lifetime

## Deployment

GitHub Actions workflows handle CI/CD:
- `deploy.yml`: Deploys to production on push to master
- `deploy-dev.yml`: Dev environment deployment
- `deploy-prod-tag.yml`: Tag-based production deployment

**Deployment process:**
1. Substitute environment variables in `zohoclient-config.yml`
2. Copy config to server at `/etc/conf/`
3. Build binary with Go 1.24
4. Deploy to `/usr/local/bin/`
5. Restart systemd service `zohoclient.service`

## Common Development Patterns

**Adding New Order Processing Logic**
- Modify `impl/core/orders.go:ProcessOrders()` method
- Use structured logging with `slog` and `sl.Err()` helper
- Update entity models in `entity/` if data structure changes
- Database changes require SQL migration (no ORM - raw SQL only)

**Adding Zoho API Endpoints**
- Add methods to `internal/services/zoho-service.go`
- Use `buildURL()` helper for path construction
- Always call `RefreshToken()` before API requests
- Unmarshal responses into entity structs with proper error handling

**Adding HTTP API Endpoints**
- Add route in `internal/http-server/api/api.go` within the router setup
- Create handler in `internal/http-server/handlers/<module>/<action>.go`
- Add interface methods to Handler interface and implement in `impl/core/`
- Use `internal/lib/api/request` for decoding/validation
- Use `internal/lib/api/response` for consistent response formatting
- All endpoints are automatically authenticated via Bearer token middleware
- Update `docs/apiv1.md` with endpoint documentation

**Logging**
- Use structured logging: `log.With(slog.String("key", "value"))`
- Sensitive data helper: `sl.Secret("token", value)` (though often commented out)
- Module tagging: `log.With(sl.Module("module-name"))`
- Telegram handler forwards logs to admins if enabled

## HTTP REST API

The application includes an HTTP REST API server for external integrations. The server runs concurrently with the order processing service.

**Architecture:**
- Built with `chi` router and `chi/render` for JSON responses
- Bearer token authentication on all endpoints (via `Authorization: Bearer <token>` header)
- Token validation against config file (`listen.key`) or in-memory cache
- Middleware stack: timeout (5s), request ID, recovery, JSON content-type, authentication
- Request/response utilities in `internal/lib/api/` with validation support

**Current Endpoints:**
- `POST /api/v1/order` - Order update endpoint (stub implementation, returns nil)

**Authentication Flow:**
1. Client sends request with `Authorization: Bearer <token>` header
2. `authenticate` middleware extracts token and validates
3. Calls `Core.AuthenticateByToken()` which checks:
   - In-memory cache (`c.keys` map)
   - Config file auth key (`c.authKey` from `listen.key`)
   - Database lookup (commented out, not implemented)
4. On success, adds `UserAuth` to request context and continues
5. On failure, returns 401 Unauthorized

See `docs/apiv1.md` for detailed API documentation.

## Known Issues and Quirks

- Order processing runs in infinite loop in main goroutine (blocking)
- HTTP API server not started in `main.go` (infrastructure exists but not wired up)
- No graceful shutdown handling for either service
- Telegram markdown escaping is incomplete (see `Sanitize()` function)
- Legacy telegram implementation in `impl/telegram/` is commented out but not removed
- No retry logic for failed Zoho order creation (orders stay in queue)
- `zoho_id` check uses placeholder string "[B2B]" instead of boolean flag
- `Core.UpdateOrder()` endpoint implementation is a stub that returns nil
- Authentication token cache (`c.keys`) is never initialized, will panic on first auth attempt
- `c.authKey` is never populated from config, authentication will always fail
