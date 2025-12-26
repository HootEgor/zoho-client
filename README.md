# Zoho Client

**OpenCart - Zoho CRM Connector**

![Go](https://img.shields.io/badge/Go-1.24-blue.svg?logo=go)
![MySQL](https://img.shields.io/badge/MySQL-8.0-blue.svg?logo=mysql)
![Telegram](https://img.shields.io/badge/Telegram-Bot%20API-blue.svg?logo=telegram)

## Project Purpose

This project is an **OpenCart - Zoho CRM connector** that synchronizes e-commerce orders from OpenCart to Zoho CRM. It automatically monitors your OpenCart database for new orders and creates corresponding contacts and sales orders in Zoho CRM via their REST API.

## Description

Zoho Client is a Go-based integration service that bridges the gap between OpenCart and Zoho CRM. The service uploads orders from OpenCart to Zoho CRM and can receive updates from Zoho via webhooks.

**Upload to Zoho CRM:** The service continuously polls your OpenCart MySQL database for new orders with specific statuses (New, Payed, PrepareForShipping) and automatically:

- Creates or finds contacts in Zoho CRM based on customer information
- Fetches product Zoho IDs from an external product repository
- Creates sales orders in Zoho CRM with line items (handling large orders by chunking items)
- Updates OpenCart orders with Zoho IDs to prevent duplicate processing
- Provides optional Telegram bot notifications for monitoring and admin commands

**Receive Updates from Zoho:** The service includes an HTTP API server that receives webhook notifications from Zoho CRM to update order statuses and line items back in OpenCart.

The service runs as a background daemon, polling every 2 minutes, and includes an HTTP API server for receiving order updates from external systems.

## Technology Stack

### Core Technologies
- **Go 1.24** - Primary programming language
- **MySQL 8.0** - OpenCart database integration
- **Zoho CRM REST API** - CRM synchronization

### Key Libraries & Frameworks
- **[Chi Router](https://github.com/go-chi/chi)** (`v5`) - HTTP router and middleware
- **[Cleanenv](https://github.com/ilyakaznacheev/cleanenv)** - Configuration management (YAML)
- **[go-playground/validator](https://github.com/go-playground/validator)** (`v10`) - Request validation
- **[go-sql-driver/mysql](https://github.com/go-sql-driver/mysql)** - MySQL database driver
- **[gotgbot](https://github.com/PaulSonOfLars/gotgbot)** (`v2`) - Telegram Bot API client
- **[google/uuid](https://github.com/google/uuid)** - UUID generation

### Additional Tools
- **Telegram Bot API** - Admin notifications and monitoring
- **REST API Server** - HTTP endpoints for order updates
- **Structured Logging** - Built-in logging with Telegram integration

## Features

*   **Automated Order Synchronization** - Monitors OpenCart database and syncs orders to Zoho CRM
*   **Contact Management** - Automatically creates or finds contacts in Zoho CRM
*   **Product Mapping** - Fetches Zoho product IDs from external product repository
*   **Large Order Handling** - Chunks orders with >100 items to comply with Zoho API limits
*   **Telegram Bot Integration** - Optional notifications and admin commands via Telegram
*   **REST API** - Bidirectional order updates via HTTP endpoints
*   **Duplicate Prevention** - Tracks processed orders to avoid duplicates
*   **Configurable Polling** - Adjustable sync intervals and order status filters

## API Endpoints

### Order Update Webhook

The service provides a webhook endpoint to receive order updates from Zoho CRM. When Zoho sends order updates (e.g., status changes, line item modifications), this endpoint updates the corresponding order in OpenCart.

- **Endpoint:** `POST /zoho/webhook/order`
- **Authentication:** Bearer token (configured in `listen.key` config section)
- **Content-Type:** `application/json`

#### Request Format

The request body must contain a `data` array with order update objects:

```json
{
  "data": [
    {
      "zoho_id": "1234567890123456789",
      "status": "Fulfilled",
      "grand_total": 1250.50,
      "ordered_items": [
        {
          "zoho_id": "9876543210987654321",
          "price": 100.00,
          "total": 100.00,
          "quantity": 1,
          "is_shipping": false
        }
      ]
    }
  ]
}
```

#### Response Format

**Success Response:**
```json
{
  "success": true,
  "status_message": "Order updated successfully",
  "timestamp": "2025-01-15T10:30:00Z"
}
```

**Error Response:**
```json
{
  "success": false,
  "error_code": "BAD_REQUEST",
  "error_message": "Invalid request format",
  "timestamp": "2025-01-15T10:30:00Z"
}
```

### Webhook Payload Structure

| Field | Format | Notes |
|-------|--------|-------|
| `data` | Array | Required. Array containing order update objects |
| `data[].zoho_id` | String | Required. Zoho CRM Sales Order ID (used to find the corresponding OpenCart order) |
| `data[].status` | String | Required. Order status name (e.g., "Fulfilled", "Cancelled"). Must match OpenCart status names |
| `data[].grand_total` | Float64 | Required. Grand total amount (must be > 0) |
| `data[].ordered_items` | Array | Required. Array of order line items (replaces all existing items) |
| `data[].ordered_items[].zoho_id` | String | Required. Zoho CRM product ID |
| `data[].ordered_items[].price` | Float64 | Required. Unit price (must be > 0) |
| `data[].ordered_items[].total` | Float64 | Required. Line item total (must be > 0) |
| `data[].ordered_items[].quantity` | Integer | Required. Quantity ordered (must be > 0) |
| `data[].ordered_items[].is_shipping` | Boolean | Optional. Set to `true` for shipping line items, `false` or omit for product items |

#### Notes

- The service updates the OpenCart order status if a valid status name is provided
- All existing line items are replaced with the items provided in `ordered_items`
- Discounts and totals are automatically recalculated based on the provided line items
- Tax rates are calculated from existing order totals or default to 23% VAT if unavailable
- Only the first order in the `data` array is processed (multiple orders require separate requests)

## Getting Started

1.  Clone the repository:
    ```bash
    git clone https://github.com/your-username/zohoclient.git
    ```
2.  Install dependencies:
    ```bash
    go mod download
    ```
3.  Configure the application by creating a `config.yml` file. You can use `config.example.yml` as a template.
4.  Build and run the application:
    ```bash
    go run cmd/zoho/main.go
    ```
