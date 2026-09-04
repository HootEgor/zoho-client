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

Configuration is managed via YAML files. The template is `config.yml`; the production configs are
`zohoclient-config.yml` (shop 1) and `zohoclient-config-ua.yml` (shop 2, the UA site), which use environment
variable placeholders for CI/CD.

**One process serves one OpenCart shop.** A second shop runs a second process with its own config
file, systemd unit (`zohoclient-ua.service`) and listen port. Nothing in the code is
multi-tenant.

Three things must never be shared between instances:
- **The log file.** `site.log_file` names it inside the `-log` directory (default `zohoclient.log`);
  it is read straight off `Config` rather than through `SiteSettings`, because the logger is built
  before the settings are resolved.
- **The Telegram bot token.** `bot/tgbot.go` receives commands via `getUpdates` long polling, and
  Telegram permits only one such connection per token — two instances sharing one would keep
  terminating each other's poll with 409 Conflict.
- **The Mongo database.** Order versions are keyed by `order_id` alone (the `orders` collection in
  `internal/database/mongo/mongo.go`) and OpenCart order ids restart from 1 per shop, so a shared
  database would mix unrelated orders into one document. The Mongo *server* can be shared.

**Key configuration sections:**
- `env`: Environment name for logging (local, production, etc.)
- `sql`: OpenCart database connection (can be disabled with `enabled: false`)
- `telegram`: Optional Telegram bot for admin notifications
- `site`: **everything that differs between shops** — status ids, B2B groups, language id, custom
  field ids, poll windows, shipping code map, and the feature flags for the optional subsystems
- `zoho`: Zoho CRM API credentials (OAuth refresh token flow) plus the picklist values written onto
  records (field API *names* stay fixed in `entity/` — all shops share one Zoho org)
- `prod_repo`: External product repository API for fetching Zoho product IDs
- `listen`: HTTP API server settings (bind IP, port, authentication key)

See `docs/config.md` for the full key reference.

**Site settings (`internal/config/site.go`)**
- `Config.SiteSettings()` resolves the `site:` and `zoho:` sections into a validated, immutable
  `SiteSettings`, applying `DefaultSiteSettings()` for every omitted key — so an old config file
  keeps its exact previous behaviour. `main.go` fails the process on a validation error and logs
  the resolved settings (`site settings resolved`).
- `SiteSettings` is injected into `sql.NewSQLClient`, `services.NewZohoService`, `core.New` and
  `api.New`. Anything shop-specific belongs there, not in a package-level constant.
- Helper methods replace what used to be free functions: `IsB2B`, `CustomerCategory`, `PostType`,
  `PaymentStatus`, `OrderStatusName`, `OrderStatusIdByName`, `TotalCode`, `AllowedCurrency`.
- Feature flags (`site.features.payments` / `customer_sync` / `b2b`, plus `smartsender.enabled`)
  make a subsystem inert, not merely idle: with one off the service never creates or reads the
  columns it owns. `orderColumns()` in `statements.go` drops the `wf_payment_*` group from every
  order SELECT when payments are off, and `scanOrderFromRows` matches.
- `impl/core/testdata/zoho_order.golden.json` pins the exact Sales Order payload for a fixed order
  under the defaults. Treat a diff there as a production behaviour change, not a test to update.
- `zoho.order_status_map` runs in both directions and they are NOT symmetric. Outbound,
  `buildZohoOrder` always stamps `NewOrderStatusName()` (the `order_statuses.new` entry) — Zoho
  owns the status after that, so deriving it from the OpenCart status would let a re-push overwrite
  a status a Zoho user had moved on. Inbound, `OrderStatusIdByName` resolves through the whole map,
  so every status a Zoho user can set needs an entry. Do not trim the map.

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
- `prod_repo.site_code` scopes the lookup: `GET /bot/product/{uid}?site={site_code}`. The repository
  holds one Zoho product id per site, so a lookup without it returns the default site's ids and Zoho
  rejects the resulting Sales Order with `FILTER_CRITERIA_NOT_SATISFIED`. Empty omits the parameter.
- Only products with an empty `oc_product.zoho_id` are fetched. A database seeded from another shop
  carries that shop's ids and must have them cleared to be re-fetched.

**bot/TgBot**
- Telegram bot for admin notifications (uses PaulSonOfLars/gotgbot library)
- Supports per-admin log level filtering via `/level` command
- Logger handler sends formatted messages to admins based on log levels

**internal/http-server/api/Server**
- HTTP REST API server built with chi router
- Listens on configured bind IP and port from `listen` config section
- Protected by Bearer token authentication middleware
- Provides `/api/v1/order` endpoint for updating orders from external systems (e.g., Zoho webhooks)
- Updates OpenCart database: order status, line items (with full replacement), recalculated discounts/totals
- See `docs/apiv1.md` for API documentation

### Data Flow

1. **Order Retrieval** (`database.GetNewOrders()`)
   - Fetches orders with the statuses in `site.order_statuses.poll` — by default New (1), Pending (2), PrepareForShipping (5), Payed (17), PaymentLinkRequest (22), PaymentLinkCreated (23)
   - The payment-link statuses are included so an order that stalls anywhere in the wfsync flow (confirmed → 2 → 22 request → 23 link created → 17 paid) still reaches Zoho even if the customer never pays; its payment record is created/updated later once wfsync reports a payment status
   - Only processes orders modified in the last `site.lookback_days` days (default 30), and skips any already synced (`zoho_id` set)
   - Excludes B2B orders (customer groups listed in `site.b2b_group_ids`)

2. **Validation** (`impl/core/orders.go:85-103`)
   - Checks for empty product UIDs (fails fast)
   - Attempts to fetch missing Zoho IDs from product repository
   - Fails if Zoho IDs still missing after fetch attempt

3. **Order Building** (`buildZohoOrder()`)
   - Converts OpenCart money values (stored as cents) to floats
   - Chunks line items beyond `zoho.chunk_size` (Zoho API limitation)
   - Adds metadata from config: `zoho.location`, `zoho.order_source`, and the Status mapped from
     the OpenCart status via `zoho.order_status_map`

4. **Zoho Sync** (`impl/core/orders.go:107-142`)
   - Creates contact (handles duplicates gracefully)
   - Creates order with initial items
   - Adds remaining items via chunked updates
   - Updates OpenCart order with Zoho ID or "[B2B]" marker

5. **API Order Update** (`impl/core/api-order.go:UpdateOrder()`)
   - Receives updates from external systems via HTTP API
   - Finds order by zoho_id in OpenCart database
   - Updates status by mapping Ukrainian status names to OpenCart status IDs
   - Replaces ALL line items (deletes existing, inserts new)
   - Recalculates discounts using same logic as order creation
   - Updates order total in database

### Important Details

**Shared Database Contract with wfsync (`~/projects/wfsync`)**
- Both services run in production against the **same OpenCart MySQL database**. wfsync handles Stripe payments + wFirma invoices; zoho-client syncs orders to Zoho CRM.
- Column ownership on `oc_order`:
  - wfsync **writes**, zoho-client **reads**: `wf_payment_status` VARCHAR(32), `wf_payment_id` VARCHAR(64), `wf_payment_amount` BIGINT (cents), `wf_payment_session` VARCHAR(128). zoho-client (re)creates these defensively in `sql-client.go` so deploy order / a fresh DB never breaks reads — **definitions must stay identical to wfsync's** (`opencart/database/sql-client.go`).
  - zoho-client owns: `zoho_id`, `zoho_payment_id`, `zoho_payment_status`, `zoho_modified_time` (order); `zoho_id` (product, customer).
- **Order status 17 coordination**: wfsync sets `order_status_id = 17` when a Stripe hold is confirmed (`requires_capture`). zoho-client polls statuses {1,5,17} and treats 17 as a sync trigger — at that point `wf_payment_status = "requires_capture"` maps to Zoho "Кошти зарезервовано" (held), which is correct.
- **Payment status vocabulary**: `entity/payment-status.go` maps every Stripe/wfsync status string wfsync can write onto a *logical* payment state (`entity.PaymentKey*`); `zoho.payment_statuses` in the config then maps those states onto the Zoho Payments picklist. Keep the entity map complete if wfsync's status values change.
- **Payment status advancement**: a Zoho Payments record is created once (`createZohoPayment`), recording the synced status in `zoho_payment_status`. `ProcessPaymentUpdates()` detects when `wf_payment_status` later advances (e.g. held → paid) and pushes the new status via `ZohoService.UpdatePaymentStatus` — so a captured payment is not left stuck at "held".

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

**Seeding a new shop**
- A shop whose database is copied from an existing one starts with years of orders the poller
  would push to Zoho. `./zohoclient -conf=… -mark-synced -apply` stamps every order with no
  `zoho_id` with the "[SKIP]" sentinel and exits; run it with the service stopped, before going
  live. Without `-apply` it only reports the count.
- `MarkUnsyncedOrders` deliberately does not touch `date_modified` — the poller filters on it, so
  touching it would make the whole history look freshly modified.

**B2B Orders**
- Identified by `customer_group_id` via `SiteSettings.IsB2B()` (config `site.b2b_group_ids`)
- Skipped from Zoho sync but marked with `zoho_id = "[B2B]"`

**Database Schema Modifications**
- Application automatically adds `zoho_id VARCHAR(64)` columns to OpenCart tables; the payment and
  customer-sync column families are only created when their feature flag is on
- Uses prepared statements stored in `statements` map for performance
- Connection pooling: 50 max open, 10 max idle, 1-hour lifetime

**API Order Updates (Reverse Sync)**
- `OrderSearchByZohoId()` - finds OpenCart order_id by Zoho ID
- `UpdateOrderItems()` - replaces all order items and recalculates:
  1. Deletes all existing order_product rows for the order
  2. Inserts new items using product zoho_id lookup
  3. Fetches shipping from order_total table
  4. Runs `RecalcWithDiscount()` to distribute discounts across items
  5. Updates each order_product with calculated discount
  6. Updates order.total with final calculated amount
- Status mapping via `GetStatusIdByName()` - reverse lookup from Ukrainian to numeric ID
- Money conversion: API receives floats, converts to cents for database storage

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
- `POST /api/v1/order` - Order update endpoint (updates OpenCart database from external systems)

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
- HTTP API server not started in `main.go` (infrastructure exists but commented out)
- No graceful shutdown handling for either service
- Telegram markdown escaping is incomplete (see `Sanitize()` function)
- Legacy telegram implementation in `impl/telegram/` is commented out but not removed
- No retry logic for failed Zoho order creation (orders stay in queue)
- `zoho_id` check uses placeholder strings ("[B2B]", "[SKIP]") instead of a boolean flag
