## API v1 Description

### Base path

Every endpoint lives under one path namespace — the health check included, so nothing this process
serves sits at the domain root. The namespace is `listen.base_path`, `zoho` by default:

```yaml
listen:
  base_path: zoho     # -> /zoho/health, /zoho/status, /zoho/webhook/order, ...
```

**Publishing two instances on one domain.** One process serves one OpenCart shop, so two shops are
two processes on two ports. Give each its own `base_path` and a reverse proxy can route both by
path alone, with no rewriting:

```yaml
# shop 1                      # shop 2
listen:                       listen:
  base_path: zoho               base_path: zoho-ua
  port: 9800                    port: 9801
```

```nginx
location /zoho/    { proxy_pass http://127.0.0.1:9800; }
location /zoho-ua/ { proxy_pass http://127.0.0.1:9801; }
```

Changing `base_path` moves every URL this instance serves, including the webhook URLs registered in
Zoho — update those at the same time. An unusable value (a doubled slash, a space, chi's `{param}`
syntax) fails at startup rather than on the first request. Paths below are written with the default
namespace; read `/zoho` as whatever `base_path` is set to.

### Authentication
To make requests to the API, you need to provide Bearer token in the `Authorization` header.
zohoclient supports two ways of token storage: in the configuration file, `listen` section, and in the OpenCart API section inside the admin panel.
```yaml
listen:
  bind_ip: 127.0.0.1
  port: 9800
  key: api-key        # API key for the ZOHOAPI service
```

### Health and Status

#### Health Check

- **Endpoint:** `GET /zoho/health`
- **Authentication:** none — a load balancer or a systemd watchdog has no token to present. This is
  the only unauthenticated route, and it deliberately discloses nothing beyond the two fields below:
  the shop name, the traffic counters and the failure reasons all live behind the token, on
  `/zoho/status`.
- **Response:** `200` while every component is up or switched off, `503` once one is down.
  ```json
  {
    "status": "ok",
    "uptime": "3d 4h"
  }
  ```
  `status` is `ok` or `degraded`.

#### Service Status

- **Endpoint:** `GET /zoho/status`
- **Authentication:** Bearer token, as every other endpoint.
- **Response:** always `200` — the caller asked for the report, and a degraded service is the
  answer rather than a failure to produce one.
  ```json
  {
    "success": true,
    "status_message": "Success",
    "timestamp": "2026-09-07T12:04:00Z",
    "data": {
      "status": "ok",
      "site": "dark",
      "env": "production",
      "dry_run": false,
      "started_at": "2026-09-04T08:00:00+02:00",
      "uptime": "3d 4h",
      "uptime_seconds": 273840,
      "features": {
        "payments": true,
        "customer_sync": true,
        "b2b": false,
        "smartsender": false
      },
      "components": [
        {"name": "database", "state": "up", "detail": "open: 2, inuse: 0, idle: 2"},
        {"name": "mongo", "state": "up"},
        {"name": "zoho", "state": "up", "detail": "token valid for 42m 10s"},
        {"name": "order-sync", "state": "up", "detail": "last poll 41s ago"}
      ],
      "orders": {
        "last_run_at": "2026-09-07T12:03:44+02:00",
        "last_run_ms": 120,
        "last_queued": 3,
        "last_synced": 3,
        "last_failed": 0,
        "total_synced": 128,
        "total_failed": 2,
        "poll_interval": "2m0s"
      }
    }
  }
  ```

**Component states.** Only `down` makes the service `degraded`.

| State | Meaning |
| --- | --- |
| `up` | Reachable / working. `detail` carries a fact worth reading. |
| `down` | Failed. `detail` carries the error. |
| `disabled` | Switched off in the configuration — working as intended. |
| `unknown` | Not established yet, and not claimed as health. A freshly started service holds no Zoho token and has completed no poll. |

**Components.**

- `database` — pings the OpenCart MySQL database (2s timeout); `detail` is the connection pool.
- `mongo` — pings the internal database, or reports `disabled` when `mongo.enabled` is false.
- `zoho` — reports the *cached* OAuth token's expiry. Zoho is never called: asking for status must
  not spend an API call. A service holding no valid token is `unknown`, not `down` — the next sync
  fetches one.
- `order-sync` — `down` when the last poller pass ended in an error, which is the earliest sign the
  sync has stopped working and the one failure a database ping cannot see. `orders.last_error` is
  kept after recovery, so the reason a past pass failed is still readable.

`orders.total_synced` counts orders that reached Zoho since the process started; `last_queued`
includes B2B orders, which are marked and skipped rather than synced.

### Product Management

#### Update or Create Product
- **Endpoint:** `/api/v1/product`
- **Method:** `POST`
- **Description:** Updates the details of a specific product. If product is not found, it will be created.
- **Request Body:**
  ```json
  {
    "data": [
        {
            "product_uid": "28ac4a2c-6f4c-11ef-b7f7-00155d018000",
            "article": "scMUSE",
            "quantity": 6,
            "price": 25,
            "active": true,
            "categories": ["29b666d4-bc22-11ee-b7b4-00155d018000"]
        }
    ]
  }
  ```
- **Response:**
  ```json
  {
    "success": true,
    "status_message": "Success",
    "timestamp": "2025-03-24T11:22:39Z"
  }
  ```

#### Update or Add Product Description
- **Endpoint:** `/api/v1/product/description`
- **Method:** `POST`
- **Description:** Updates the description of a specific product.
- **Request Body:**
  ```json
    {
    "data": [
            {
                "language_id": 1,
                "product_uid": "28ac4a2c-6f4c-11ef-b7f7-00155d018000",
                "name": "Spa candle MUSE, 30 g",
                "description": "The candle is made of natural soy wax. The aroma of the candle is a combination of the scents of the forest and the sea. The candle is packed in a beautiful gift box."  
            }
        ]
    }
  ```

### Categories. Products Hierarchy

#### Update or Create Category
- **Endpoint:** `/api/v1/category`
- **Method:** `POST`
- **Description:** Updates category. If category is not found, it will be created.
- **Request Body:**
  ```json
  {
    "data": [
        {
            "sort_order": 0,
            "active": true,
            "parent_uid": "",
            "menu": true,
            "category_uid": "6666bc6a-a487-11e9-b6d3-00155d010d00",
            "article": ""
        }
    ]
  }
  ```

#### Update or Add Category Description
- **Endpoint:** `/api/v1/category/description`
- **Method:** `POST`
- **Description:** Updates category description.
- **Request Body:**
  ```json
  {
    "data": [
        {
            "language_id": 1,
            "category_uid": "6666bc6a-a487-11e9-b6d3-00155d010d00",
            "name": "ALL FOR EXTENSION",
            "description": "The category includes all the necessary materials for hair extension."
        }
    ]
  }
  ```

#### Get Products
- **Endpoint:** `/api/v1/product/{uid}`
- **Method:** `GET`
- **Description:** Retrieves a product data, a record from the database.
- **Response:**
  ```json
  {
    "data": [
        {
            "batch_uid": "",
            "date_added": "2024-10-24T11:52:25Z",
            "date_available": "2024-10-21T00:00:00Z",
            "date_modified": "2025-03-24T09:33:42Z",
            "ean": "",
            "height": "0.00000000",
            "image": "import/563235c5-8ab8-11ef-b7fb-00155d018000.png",
            "isbn": "",
            "jan": "",
            "length": "0.00000000",
            "length_class_id": 1,
            "location": "",
            "manufacturer_id": 0,
            "max_discount": "0.00",
            "meta_robots": "",
            "minimum": 1,
            "model": "doilon3",
            "mpn": "",
            "points": 0,
            "price": "0.0000",
            "product_id": 5970,
            "product_uid": "02bc1ea8-70d3-11ef-b7f7-00155d018000",
            "quantity": 354,
            "sku": "",
            "sort_order": 0,
            "status": 1,
            "stock_status_id": 7,
            "subtract": 1,
            "tax_class_id": 9,
            "upc": "",
            "viewed": 0,
            "weight": "0.00000000",
            "weight_class_id": 1,
            "width": "0.00000000"
        }
    ],
    "success": true,
    "status_message": "Success",
    "timestamp": "2025-03-24T09:36:34Z"
    }
    ```

### Webhooks

#### B2B Portal Webhook
- **Endpoint:** `/zoho/webhook/b2b`
- **Method:** `POST`
- **Description:** Receives webhooks from B2B portal and creates Zoho Deals.
- **Request Body:**
  ```json
  {
    "data": [
      {
        "event": "order_confirmed",
        "timestamp": "2024-01-15T10:30:00Z",
        "data": {
          "order_uid": "ord_abc123def456",
          "order_number": "1-1234",
          "client_uid": "cli_xyz789",
          "store_uid": "store_001",
          "status": "new",
          "total": 1249.99,
          "subtotal": 1041.66,
          "total_vat": 208.33,
          "discount_percent": 10,
          "discount_amount": 115.74,
          "currency_code": "USD",
          "shipping_address": "123 Main St, New York, NY 10001",
          "comment": "Please deliver before noon",
          "created_at": "2024-01-15T10:29:45Z",
          "items": [
            {
              "product_uid": "prod_001",
              "product_sku": "SKU-12345",
              "quantity": 2,
              "price": 574.99,
              "discount": 10,
              "price_discount": 517.49,
              "tax": 103.50,
              "total": 621.48
            }
          ],
          "client_name": "John Doe",
          "client_email": "john@example.com",
          "client_phone": "+1234567890"
        }
      }
    ]
  }
  ```
- **Response (Success):**
  ```json
  {
    "data": {
      "zoho_id": "5234567890123456789"
    },
    "success": true,
    "status_message": "Success",
    "timestamp": "2024-01-15T10:30:05Z"
  }
  ```
- **Response (Error):**
  ```json
  {
    "success": false,
    "status_message": "Error",
    "timestamp": "2024-01-15T10:30:05Z",
    "error": {
      "code": "INTERNAL_ERROR",
      "message": "Failed to process B2B webhook"
    }
  }
  ```

#### Payments Webhook

- **Endpoint:** `/zoho/webhook/payment`
- **Method:** `POST`
- **Feature flag:** the route exists only when `site.features.payments` is on. A shop that runs
  without the payments subsystem holds no `zoho_payment_*` columns to read the payload against, so
  a call there 404s rather than recording payloads nothing will read.
- **Description:** Zoho reports the whole Payments list of one Sales Order whenever a record in it
  changes. **Recording only** — nothing from this payload reaches OpenCart yet. Each payload is
  resolved to an OpenCart order, logged against what OpenCart already holds (the wfsync
  `wf_payment_status` and the `zoho_payment_id` this service created), and stored in MongoDB under
  that order id in the `payments` collection. Which fields are worth transferring is decided from
  the payloads real shops send.
- **Request Body:** the envelope is the usual one; `data` may be a single object or an array of
  them. `data.zoho_id` is the **Sales Order** id — it is what the order is found by — and each
  entry of `data.payments` is a Zoho Payments record as the module holds it. Ids arrive quoted or
  as bare JSON numbers depending on the shop and both are accepted; the whole record is kept, the
  `$`-prefixed system fields included, so the fields below are the ones read, not the ones allowed.
  ```json
  {
    "method": "payments.update",
    "data": {
      "zoho_id": 739178000065138138,
      "payments": [
        {
          "zoho_id": "739178000065068174",
          "order_id": 739178000065138138,
          "Name": "Payments #order-739178000065138138",
          "Status": "Створено",
          "Sum": 2500.35,
          "Currency": "UAH",
          "Exchange_Rate": 1,
          "payment_datetime": "2026-09-08T13:29:04+02:00",
          "Created_Time": "2026-09-08T11:29:04+02:00",
          "Modified_Time": "2026-09-08T11:29:04+02:00",
          "Stripe_PaymentIntent_ID": null,
          "Stripe_Checkout_Session_ID": null,
          "payment_error": null,
          "paymentLink": null,
          "rrn": null
        }
      ]
    }
  }
  ```
  Note `payment_datetime` here against the `Payment_time` this service writes when it *creates* a
  Payments record — the inbound and outbound field names are not the same.
- **Response (Success):** the count is of payment records, across every `data` entry.
  ```json
  {
    "success": true,
    "status_message": "1 payment(s) recorded",
    "timestamp": "2026-09-08T11:29:05Z"
  }
  ```
- **Unknown Sales Order:** answered `200`. A payload whose `zoho_id` matches no OpenCart order has
  nothing to be filed under and is logged and dropped; failing it would only have Zoho redeliver it
  forever. This is also what a dry-run instance sees, since it records no `zoho_id` at all.
- **Dry run:** recording still happens. MongoDB is this service's own store, not a write to Zoho or
  to the shop, and skipping it would leave the endpoint doing nothing.

### Order Retrieval (Coming Soon)