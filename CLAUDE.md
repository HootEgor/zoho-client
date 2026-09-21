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

Four things must never be shared between instances:
- **The log file.** `site.log_file` names it inside the `-log` directory (default `zohoclient.log`);
  it is read straight off `Config` rather than through `SiteSettings`, because the logger is built
  before the settings are resolved.
- **The Telegram bot token.** `bot/tgbot.go` receives commands via `getUpdates` long polling, and
  Telegram permits only one such connection per token — two instances sharing one would keep
  terminating each other's poll with 409 Conflict.
- **The Mongo database.** Order versions are keyed by `order_id` alone (the `orders` collection in
  `internal/database/mongo/mongo.go`) and OpenCart order ids restart from 1 per shop, so a shared
  database would mix unrelated orders into one document. The Mongo *server* can be shared.
- **`listen.base_path`**, but only when both instances are published on one domain. Every endpoint
  lives under that one namespace, so two instances keeping the default both answer at `/zoho/...`
  and a reverse proxy cannot route between them. Changing it moves the webhook URLs registered in
  Zoho too.

**Key configuration sections:**
- `env`: Environment name for logging (local, production, etc.)
- `sql`: OpenCart database connection (can be disabled with `enabled: false`)
- `telegram`: Optional Telegram bot for admin notifications
- `site`: **everything that differs between shops** — status ids, B2B groups, language id, custom
  field ids, poll windows, shipping code map, and the feature flags for the optional subsystems
- `zoho`: Zoho CRM API credentials (OAuth refresh token flow) plus the picklist values written onto
  records (field API *names* stay fixed in `entity/` — all shops share one Zoho org)
- `listen.base_path`: the single path namespace every endpoint is served under (default `zoho`)
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
  columns it owns.
- `site.payments.source` is a second, orthogonal axis: the flag says *whether* payments are synced,
  the source says *who writes* `oc_order.wf_payment_*` and therefore in what vocabulary they are
  read — `wfsync` (Stripe strings, the default) or `tranzzo` (the UA shop's own OpenCart module:
  `init`/`auth`/`capture`/`void`). **Both sources use the same four columns**, so the database
  layer still guards on `.Payments`; the source only picks the map.
- `SiteSettings.PaymentStatus()` dispatches on the source. The two vocabularies are disjoint and an
  unknown value falls through to `entity.PaymentKeyError`, i.e. a live payment written to Zoho as
  *Помилка операції* — silent and indistinguishable from a real failure. Keep both maps in
  `entity/payment-status.go` complete; `TestSiteSettings_PaymentStatusVocabularyFollowsSource`
  pins the split.
- The tranzzo flow is **bi-directional**. Inbound, `wf_payment_*` is read like any other source.
  Outbound, `Core.notifyTranzzo` (`impl/core/tranzzo.go`) enqueues a row in `oc_tranzzo_queue`
  after `UpdateOrder` commits — from *both* write paths, because a cancellation usually arrives as
  a status-only webhook. The module's cron worker picks it up within a minute. See
  **Tranzzo queue contract** below. Takeover (`zoho_managed`) fires from the *payment* webhook, not
  the order one — see `detectZohoPaymentTakeover`, which also writes `wf_payment_*` back once
  control has passed over.
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
- Order version history (admins only, both commands are no-ops when `mongo.enabled` is false):
  `/versions <order_id>` lists the stored versions of an order (id + timestamp, last 30),
  `/version <order_id> <version_id>` shows one version's status, total and product count
- `/status` renders the same snapshot as `GET /zoho/status` — component states, features and what
  the order poller last did
- `bot.VersionRepository` (Mongo client) and `bot.StatusProvider` (`Core`) are handed over in
  `main.go` *before* `tgBot.Start()`, so polling begins only once the command handlers have
  something to read
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
- Applies to a shop with `site.payments.source: wfsync` (shop 1). The UA shop is on `tranzzo` and
  shares no database with wfsync at all.
- Both services run in production against the **same OpenCart MySQL database**. wfsync handles Stripe payments + wFirma invoices; zoho-client syncs orders to Zoho CRM.
- Column ownership on `oc_order`:
  - wfsync **writes**, zoho-client **reads**: `wf_payment_status` VARCHAR(32), `wf_payment_id` VARCHAR(64), `wf_payment_amount` BIGINT (cents), `wf_payment_session` VARCHAR(128). zoho-client (re)creates these defensively in `sql-client.go` so deploy order / a fresh DB never breaks reads — **definitions must stay identical to wfsync's** (`opencart/database/sql-client.go`).
  - zoho-client owns: `zoho_id`, `zoho_payment_id`, `zoho_payment_status`, `zoho_modified_time` (order); `zoho_id` (product, customer).
- **Order status 17 coordination**: wfsync sets `order_status_id = 17` when a Stripe hold is confirmed (`requires_capture`). zoho-client polls statuses {1,5,17} and treats 17 as a sync trigger — at that point `wf_payment_status = "requires_capture"` maps to Zoho "Кошти зарезервовано" (held), which is correct.
- **Payment status vocabulary**: `entity/payment-status.go` maps every Stripe/wfsync status string wfsync can write onto a *logical* payment state (`entity.PaymentKey*`); `zoho.payment_statuses` in the config then maps those states onto the Zoho Payments picklist. Keep the entity map complete if wfsync's status values change.
- **Payment status advancement**: a Zoho Payments record is created once (`createZohoPayment`), recording the synced status in `zoho_payment_status`. `ProcessPaymentUpdates()` detects when `wf_payment_status` later advances (e.g. held → paid) and pushes the new status via `ZohoService.UpdatePaymentStatus` — so a captured payment is not left stuck at "held".

**Tranzzo queue contract (UA shop, `site.payments.source: tranzzo`)**
- `oc_tranzzo_queue` is the *only* way into the shop's payment module — it has no HTTP endpoint
  (removed in its T-015). We INSERT; we never read the table back.
- We set `method` (`zoho_order`), `code`, `root_code` (`order_<opencart order id>`) and `payload`.
  Every other column has a database default that is already what a fresh task needs: `status` `'N'`,
  `attempt` `0`, `date_insert` `CURRENT_TIMESTAMP`. The worker moves it `N` → `W` → `F`, retrying
  up to 5 times before `E`.
- `code` is not in the contract as it was described to us, but the module's dedup index is
  `(status, method, code)` and `findActiveTask()` matches all three, so `tranzzoEventCode()` fills
  it. Takeover gets its own code: the module exempts `zoho_managed` from its duplicate check
  precisely because such an event repeats an already-seen status and sum.
- `payload.sum` is in **major units** — the module converts with `sumToMinor`. Every other amount
  in this service is cents. `wf_payment_amount`, read in the other direction, *is* cents.
- **The status string is matched as text against the module's own admin setting, not against
  `zoho.order_status_map`.** The two configurations are coupled by nothing but agreeing on a
  phrase, and the live UA picklist (`Опрацювання замовлення`, `Відмінено`) does not match the
  module's shipped defaults (`Прийнятий, очікується оплата`, `Отмена заказа`).
  `TestUAStatusMapCoversTheTranzzoTriggers` is the reminder.
- A cancellation carries `cancel: true` as well as the status, because the flag overrides the text
  match. Capture has no such flag and depends entirely on the wording agreeing.
- `Cancel` follows the status *actually applied* (`newStatusId == site.StatusCanceled`), not the
  words that arrived: an unresolvable status leaves the order where it was, and the event must not
  then claim a cancellation the shop did not make.
- Enqueueing is deliberately non-fatal. The order update is already committed, and returning an
  error would turn it into a 500 that Zoho retries against an update that already landed.
- Dry-run does not enqueue: the module acts on these rows with real money.
- **Takeover** (`zoho_managed`) is raised by `detectZohoPaymentTakeover` in `api-payment.go`: a
  payments webhook listing a record this service did not create means a manager raised a payment
  inside Zoho, which is the moment control passes over. `createdHere()` decides ownership by the
  recorded `zoho_payment_id` *and* by the `Name` this service gives its records
  (`zohoPaymentName`), the latter covering the window between `CreatePayment` returning an id and
  `UpdateOrderZohoPayment` storing it. That second check matters because the module latches the
  flag irreversibly: a missed takeover is recoverable, a false one is not.
- **After a takeover the write-back reverses**: `writeZohoPaymentState` fills `wf_payment_status`
  / `_id` / `_amount` from the payment Zoho now drives, *before* enqueueing the task the module
  reads them on. Never before a takeover — the module owns those columns until then, and two
  writers on one column is what the latch exists to prevent.
- Zoho holds **one active payment per Sales Order**: raising a new one cancels the previous, so a
  correction arrives as two webhooks (the cancellation, then the new payment). `activePayment()`
  takes the last unsettled record, falling back to the last settled one — a list where everything
  has settled is itself the answer, the payment really is off.
- `entity.TranzzoStatusForKey` is the reverse map and is deliberately **not** a bijection: seven
  logical states collapse onto the module's four words (`in_progress`→`init`, `refunded`→`void`
  alongside `canceled`, `error`→`init`, which is where the module files a failed attempt itself).
  An unmapped picklist value writes **nothing**: an empty `wf_payment_status` reads as "no payment"
  to both sides, so blanking it would erase a state rather than report it.
- `wf_payment_id` then carries the *Zoho Payments record id* — a payment raised in Zoho has no
  Tranzzo transaction behind it, and the module only displays the column.
- The takeover event is sent on **every** qualifying webhook, not once. The module's own UPDATE is
  guarded by `zoho_managed = 0` and it exempts these events from its duplicate check, so a repeat
  is free — and it earns its place, because after takeover a queue task is the only moment the
  module looks at the order at all (`applyZohoPaymentState()` reads `wf_payment_*` exactly there,
  with no polling in between).

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

**Dry run**
- `dry_run: true` (top level, not under `site:`) stops every write — to Zoho *and* to OpenCart —
  while leaving polling, validation and product resolution running; the built Sales Order is logged
  instead of sent.
- Crucially it does not record `zoho_id`, so nothing is marked synced and the queued orders sync
  normally once it is switched off. `processOrder` returns an empty id in this mode, and both
  callers treat that as "nothing to record" — `PushOrderToZoho` must not write it back or it would
  wipe an existing id.
- Inbound webhooks are received, decoded, validated and diffed as usual, but `Core.UpdateOrder`
  returns before both write paths (status-only and the full transaction) and logs what it would
  have done. It must also skip `SetOrderZohoModifiedTime`: storing that timestamp would make the
  same webhook look like an echo and be dropped once dry-run is off. `ProcessB2BWebhook` likewise
  builds the Deal and returns an empty id without creating it.
- Because no `zoho_id` is ever recorded, a webhook for an order the mode "synced" finds nothing.
  That is expected, so under dry-run a `sql.ErrOrderNotFound` is warned and the update returns
  `nil` (a `200`) instead of a `DATABASE_ERROR` and a `500`. Only that sentinel is forgiven — a
  database that cannot answer still fails, or an outage would read as a quiet run. The lookup also
  runs once instead of five times: the retry exists for the race with the `zoho_id` write, and
  dry-run performs no such write.

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

**Inbound payload quirks (`POST /zoho/webhook/order`)**
- `zoho_id` arrives as a JSON *string* from one shop and as a bare JSON *number* from another (the
  UA site's Deluge function does not quote the Sales Order id, though it quotes the subform ids).
  `ApiOrder`/`ApiOrderedItem` have custom `UnmarshalJSON` that accept both.
- A Zoho id is 18 digits and float64 carries 15, so it must never be decoded as a number:
  `739178000064455061` comes back as `739178000064455000` and matches no order. Two things keep
  that from happening and both are load-bearing — `request.DecodeWithBody` decodes the envelope
  with `UseNumber()` (`Request.Data` is an `interface{}` that gets re-marshalled into the struct),
  and `entity.zohoID` takes the digits from the literal. `TestRouter_NumericZohoIdSurvivesTheEnvelope`
  fails with the corrupted id if either is removed.
- A decode or validation failure logs the raw body as `payload` (`request.Snippet`, first 4 KB), so
  a shop sending an unexpected shape can be diagnosed from the log alone.

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

**Base path.** Every route is mounted under `listen.base_path` (default `zoho`) — the health check
included, so nothing this process serves sits at the domain root. `api.NormalizeBasePath` rejects an
unmountable value at startup rather than on the first request. Two instances published on one domain
need different values here; the paths below assume the default.

**Current Endpoints:**
- `GET /zoho/health` - liveness probe. **The only unauthenticated route**, so it returns nothing but
  `status` and `uptime`; `503` once a component is down. The auth middleware is scoped to a
  `router.Group` around everything else, so a route added there cannot accidentally be published
  unauthenticated.
- `GET /zoho/status` - full service status (authenticated), same snapshot as the bot's `/status`
- `POST /zoho/webhook/order` - Order update endpoint (updates OpenCart database from external systems)
- `POST /zoho/webhook/payment` - Payments list of one Sales Order; the route exists only when
  `site.features.payments` is on. **Recording only** - the payload is resolved to an OpenCart
  order, logged against `wf_payment_status` / `zoho_payment_id`, and stored in the Mongo `payments`
  collection. Nothing reaches OpenCart yet; what to transfer is decided from real payloads. An
  unknown `zoho_id` answers `200` (nothing to file it under), and dry-run still records, because
  Mongo is this service's own store rather than a write to Zoho or the shop.
- `POST /zoho/webhook/b2b` - B2B portal webhook; the route exists only when `site.features.b2b` is on
- `GET /zoho/push/order/{id}` - push one order to Zoho on demand

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

**Service status (`impl/core/status.go`)**
- `Core.Status()` is the single source for the `/health` endpoint, `GET /zoho/status` and the bot's
  `/status`. It pings MySQL and Mongo in parallel (2s each, inside the router's 5s timeout) but
  **never calls Zoho** — `ZohoService.TokenStatus()` reads the cached token's expiry instead, so
  asking for status cannot spend a Zoho API call.
- Component state `unknown` exists so a freshly started service (no token yet, no poll completed)
  does not report health it cannot vouch for, and is not counted as degraded. Only `down` is.
- `ProcessOrders` calls `recordOrderRun` on every pass; the stats live behind `Core.orderSyncMu`
  because the poll goroutine writes them and the HTTP/Telegram goroutines read them.
- `ZohoService.tokenMu` guards the access token, its expiry and the API domain. `send()` reads the
  token and the domain together, so a concurrent refresh cannot pair one request's token with
  another's domain.

## Known Issues and Quirks

- Order processing runs in infinite loop in main goroutine (blocking)
- HTTP API server not started in `main.go` (infrastructure exists but commented out)
- No graceful shutdown handling for either service
- Telegram markdown escaping is incomplete (see `Sanitize()` function)
- Legacy telegram implementation in `impl/telegram/` is commented out but not removed
- No retry logic for failed Zoho order creation (orders stay in queue)
- `zoho_id` check uses placeholder strings ("[B2B]", "[SKIP]") instead of a boolean flag
