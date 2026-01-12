## API v1 Description

### Authentication
To make requests to the API, you need to provide Bearer token in the `Authorization` header.
zohoclient supports two ways of token storage: in the configuration file, `listen` section, and in the OpenCart API section inside the admin panel.
```yaml
listen:
  bind_ip: 127.0.0.1
  port: 9800
  key: api-key        # API key for the ZOHOAPI service
```

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

### Order Retrieval (Coming Soon)