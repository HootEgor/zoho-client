# Plan: Rework UpdateOrder to Calculate Tax and Update Totals

## Summary

Rework the `UpdateOrder()` flow to move business logic to core package:
1. Calculate tax rate from existing order totals (tax/sub_total rounded to 4 decimals) - **in core**
2. Apply tax to each line item during insertion - **in core**
3. Calculate discount percentage from API item totals vs full item totals - **in core**
4. Update all entries in `order_total` table (sub_total, tax, discount, shipping, total) - **via database layer**
5. Update `order.total` field with calculated total - **via database layer**

**Architecture Change**:
- **Business logic** → `impl/core/api-order.go`
- **Data access** → `internal/database/sql-client.go`

**Key Changes**:
- Move calculation logic from `UpdateOrderItems()` to `UpdateOrder()` in core
- Keep `UpdateOrderItems()` as simple data access function
- Add helper database methods for granular operations
- Create `OrderProductData` struct in database package

## Overview
The current implementation mixes business logic with data access. This rework separates concerns:
- **Core layer**: Tax calculations, discount calculations, orchestration
- **Database layer**: CRUD operations on order_product and order_total tables

## Current Implementation Issues

The current `UpdateOrderItems()` function in `internal/database/sql-client.go:491-592`:
1. ✅ Deletes all existing order products
2. ✅ Inserts new products from API
3. ❌ Sets `tax=0` for all items (doesn't calculate per-item tax)
4. ❌ Preserves old tax from `order_total` table (doesn't update)
5. ❌ Doesn't update `order_total` table at all (sub_total, discount, etc.)
6. ✅ Updates only `order.total` field

## Required Changes

### Part A: Database Layer (Data Access Only)

All changes in `internal/database/sql-client.go` - keep methods simple, no business logic.

#### A1: Add GetOrderProductTotals() - Query sum of inserted products
**Location**: After `OrderTotal()` function (line ~403)

```go
func (db *MySql) GetOrderProductTotals(orderId int64) (totalSum int64, taxSum int64, error)
```

**Logic**:
```sql
SELECT SUM(total), SUM(tax) FROM {prefix}order_product WHERE order_id = ?
```
Returns sums in cents (as stored in DB).

#### A2: Add UpdateOrderTotal() - Update single row in order_total table
**Location**: After `GetOrderProductTotals()`

```go
func (db *MySql) UpdateOrderTotal(orderId int64, code string, title string, valueInCents int64, sortOrder int) error
```

**Logic**:
```sql
INSERT INTO {prefix}order_total (order_id, code, title, value, sort_order)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE value = VALUES(value), title = VALUES(title)
```

Handles a single total entry (sub_total, tax, discount, shipping, or total).

#### A3: Simplify UpdateOrderItems() - Remove business logic
**Location**: Lines 491-592

**New signature**:
```go
func (db *MySql) UpdateOrderItems(orderId int64, items []OrderProductData, currencyValue float64, orderTotal float64) error
```

**New input struct** (define before function):
```go
type OrderProductData struct {
    ZohoID       string
    Quantity     int
    PriceInCents int64  // Per-unit price, in cents
    TotalInCents int64  // Line total, in cents
    TaxInCents   int64  // unit tax, in cents
}
```

**Simplified logic**:
1. Delete all products: `DELETE FROM order_product WHERE order_id = ?`
2. For each item, INSERT with provided values (no calculations):
   ```sql
   INSERT INTO {prefix}order_product (order_id, product_id, name, model, quantity, price, total, tax, reward)
   SELECT ?, p.product_id, pd.name, p.model, ?, ?, ?, ?, 0
   FROM {prefix}product p
   JOIN {prefix}product_description pd ON p.product_id = pd.product_id
   WHERE p.zoho_id = ? AND pd.language_id = 2
   ```
   Parameters: `orderId, item.Quantity, item.PriceInCents, item.TotalInCents, item.TaxInCents, item.ZohoID`
3. Update order.total:
   ```sql
   UPDATE {prefix}order SET date_modified = ?, total = ? WHERE order_id = ?
   ```

**Remove**:
- All tax calculation logic
- All discount calculation logic
- `RecalcWithDiscount()` call
- Totals calculation
- order_total updates

This function now only does CRUD operations, no business logic.

### Part B: Core Layer (Business Logic)

All changes in `impl/core/api-order.go` - orchestrate the update with calculations.

#### B1: Completely rewrite UpdateOrder() method
**Location**: `impl/core/api-order.go:10-71`

**New implementation flow**:

```go
func (c *Core) UpdateOrder(orderDetails *entity.ApiOrder) error {
    log := c.log.With(sl.Module("core/api-order"))

    // 1. Validate input
    if orderDetails.ZohoID == "" {
        return fmt.Errorf("zoho_id is required")
    }

    // 2. Find order in database
    orderId, orderParams, currencyValue, err := c.db.OrderSearchByZohoId(orderDetails.ZohoID)
    if err != nil {
        return fmt.Errorf("order not found: %w", err)
    }

    // 3. Update status if provided
    if orderDetails.Status != "" {
        statusId := c.GetStatusIdByName(orderDetails.Status)
        if statusId > 0 {
            err = c.db.ChangeOrderStatus(orderId, statusId, "Updated via API")
            if err != nil {
                return fmt.Errorf("failed to update status: %w", err)
            }
        }
    }

    // 4. Calculate tax rate from existing order totals
    taxRate, err := c.calculateTaxRate(orderId, currencyValue)
    if err != nil {
        log.Warn("failed to calculate tax rate, using default", sl.Err(err))
        taxRate = 0.23  // Default 23% VAT
    }

    // 5. Calculate discount percentage from API items
    discountPercent := c.calculateDiscountPercent(orderDetails.OrderedItems)

    itemsTotal := 0
    taxTotal := 0
    // 6. Prepare product data with calculated tax
    productData := make([]database.OrderProductData, 0, len(orderDetails.OrderedItems))
    for _, item := range orderDetails.OrderedItems {
        // Calculate tax per unit
        taxPerUnit := item.Price * taxRate

        // Calculate line total (price × quantity, no discount)
        lineTotal := item.Price * float64(item.Quantity)

        // Convert to cents
        productData = append(productData, database.OrderProductData{
            ZohoID:       item.ZohoID,
            Quantity:     item.Quantity,
            PriceInCents: int64(math.Round(item.Price * 100)),
            TotalInCents: int64(math.Round(lineTotal * 100)),
            TaxInCents:   int64(math.Round(taxPerUnit * 100)),
        })
		
		itemsTotal += int(math.Round(lineTotal * 100))
		taxTotal += int(math.Round(taxPerUnit * 100))
    }

    // 7. Update order items in database (simplified CRUD operation)
    orderTotal := orderDetails.GrandTotal
    if orderTotal == 0 {
        orderTotal = orderParams.Total
    }
    err = c.db.UpdateOrderItems(orderId, productData, currencyValue, orderTotal)
    if err != nil {
        return fmt.Errorf("failed to update order items: %w", err)
    }

    // 8. Calculate order totals
    subTotal := itemsTotal

    // 9. Get existing shipping and titles
    shippingTitle, shippingValueCents, err := c.db.OrderTotal(orderId, totalCodeShipping, currencyValue)
    if err != nil {
        shippingTitle = "Shipping"
        shippingValueCents = 0
    }
    shipping := float64(shippingValueCents)

    taxTitle, _, _ := c.db.OrderTotal(orderId, totalCodeTax, currencyValue)
    if taxTitle == "" {
        taxTitle = "VAT"
    }

    discountTitle, _, _ := c.db.OrderTotal(orderId, discountCode, currencyValue)
    if discountTitle == "" {
        discountTitle = "Discount"
    }

    // 10. Calculate discount and final total
    discount := (subTotal + taxTotal + shipping) * discountPercent
    total := subTotal + taxTotal + shipping - discount

    // 11. Update all entries in order_total table
    err = c.updateOrderTotals(orderId, subTotal, taxTotal, discount, shipping, total,
        taxTitle, discountTitle, shippingTitle, currencyValue)
    if err != nil {
        return fmt.Errorf("failed to update order totals: %w", err)
    }

    log.Info("order updated successfully",
        slog.String("zoho_id", orderDetails.ZohoID),
        slog.Int64("order_id", orderId),
        slog.Float64("total", total))

    return nil
}
```

#### B2: Add calculateTaxRate() helper method
**Location**: After `UpdateOrder()` in `impl/core/api-order.go`

```go
func (c *Core) calculateTaxRate(orderId int64, currencyValue float64) (float64, error) {
    // Get sub_total and tax from order_total table
    _, subTotalCents, err := c.db.OrderTotal(orderId, subTotalCode, currencyValue)
    if err != nil {
        return 0, fmt.Errorf("failed to get sub_total: %w", err)
    }

    _, taxCents, err := c.db.OrderTotal(orderId, totalCodeTax, currencyValue)
    if err != nil {
        return 0, fmt.Errorf("failed to get tax: %w", err)
    }

    if subTotalCents == 0 {
        return 0, fmt.Errorf("sub_total is zero")
    }

    // Calculate rate and round to 4 decimals
    rate := float64(taxCents) / float64(subTotalCents)
    return math.Round(rate * 10000) / 10000, nil
}
```

#### B3: Add calculateDiscountPercent() helper method
**Location**: After `calculateTaxRate()`

```go
func (c *Core) calculateDiscountPercent(items []entity.ApiOrderedItem) float64 {
    var sumApiTotals float64 = 0
    var sumFullTotals float64 = 0

    for _, item := range items {
        sumApiTotals += item.Total  // Discounted total from API
        sumFullTotals += item.Price * float64(item.Quantity)  // Full price
    }

    if sumFullTotals == 0 {
        return 0
    }

    return 1.0 - (sumApiTotals / sumFullTotals)
}
```

#### B4: Add updateOrderTotals() helper method
**Location**: After `calculateDiscountPercent()`

```go
func (c *Core) updateOrderTotals(orderId int64, subTotal, tax, discount, shipping, total float64,
    taxTitle, discountTitle, shippingTitle string, currencyValue float64) error {

    // Convert to cents
    subTotalCents := int64(math.Round(subTotal * currencyValue * 100))
    taxCents := int64(math.Round(tax * currencyValue * 100))
    discountCents := int64(math.Round(discount * currencyValue * 100))
    shippingCents := int64(math.Round(shipping * currencyValue * 100))
    totalCents := int64(math.Round(total * currencyValue * 100))

    // Update each total entry
    err := c.db.UpdateOrderTotal(orderId, subTotalCode, "Suma cząstkowa", subTotalCents, 1)
    if err != nil {
        return err
    }

    err = c.db.UpdateOrderTotal(orderId, totalCodeTax, taxTitle, taxCents, 2)
    if err != nil {
        return err
    }

    err = c.db.UpdateOrderTotal(orderId, discountCode, discountTitle, discountCents, 3)
    if err != nil {
        return err
    }

    err = c.db.UpdateOrderTotal(orderId, totalCodeShipping, shippingTitle, shippingCents, 4)
    if err != nil {
        return err
    }

    err = c.db.UpdateOrderTotal(orderId, totalCodeTotal, "Razem", totalCents, 5)
    if err != nil {
        return err
    }

    return nil
}
```

## Implementation Flow Summary

The new architecture separates concerns cleanly:

**Core Layer** (`impl/core/api-order.go`):
1. Validate input (zoho_id required)
2. Find order in database
3. Update status if provided
4. Calculate tax rate from existing order_total
5. Calculate discount percentage from API items
6. Prepare product data with calculated tax
7. Call database to update items (CRUD only)
8. Get sums from inserted products (CRUD only)
9. Calculate order totals (business logic)
10. Get existing shipping and titles (CRUD only)
11. Calculate discount and final total (business logic)
12. Update all entries in order_total table (CRUD only)

**Database Layer** (`internal/database/sql-client.go`):
- `UpdateOrderItems()` - Delete old products, insert new products, update order.total
- `GetOrderProductTotals()` - Query SUM(total), SUM(tax)
- `UpdateOrderTotal()` - UPSERT single row in order_total table
- `OrderTotal()` - Fetch single row from order_total table (already exists)

## Files to Modify

### 1. **internal/database/sql-client.go** (Database Layer - Data Access Only)

#### Add new struct (line ~490, before UpdateOrderItems):
```go
type OrderProductData struct {
    ZohoID       string
    Quantity     int
    PriceInCents int64  // Per-unit price with tax, in cents
    TotalInCents int64  // Line total, in cents
    TaxInCents   int64  // Line tax, in cents
}
```

#### Add GetOrderProductTotals() - After OrderTotal() at line ~403
Query sum of totals and taxes from order_product table.
Returns `(totalSum int64, taxSum int64, error)`

#### Add UpdateOrderTotal() - After GetOrderProductTotals()
UPSERT single row in order_total table using INSERT...ON DUPLICATE KEY UPDATE.
Parameters: `(orderId, code, title, valueInCents, sortOrder)`

#### Modify UpdateOrderItems() - Lines 491-592
**New signature**:
```go
func (db *MySql) UpdateOrderItems(orderId int64, items []OrderProductData, currencyValue float64, orderTotal float64) error
```

**Simplified implementation**:
1. Delete all products
2. Insert each item (values already calculated by core layer)
3. Update order.total

**Remove**:
- All tax calculations
- All discount calculations
- `RecalcWithDiscount()` call
- `CheckoutParams` usage
- order_total updates

This becomes a simple CRUD function (~50 lines instead of 100+).

### 2. **impl/core/api-order.go** (Core Layer - Business Logic)

#### Completely rewrite UpdateOrder() - Lines 10-71
Move all business logic here from database layer.
See detailed implementation in Part B above.

**New flow**:
1. Validate and find order
2. Update status if needed
3. Calculate tax rate (call database for data, calculate in core)
4. Calculate discount percentage (pure calculation, no database)
5. Prepare OrderProductData with calculated taxes
6. Call db.UpdateOrderItems() with prepared data
7. Get product totals from database
8. Calculate order totals (business logic in core)
9. Get existing titles from database
10. Update order_total table via database calls

#### Add calculateTaxRate() - After UpdateOrder()
Business logic to calculate tax rate from order_total data.

#### Add calculateDiscountPercent() - After calculateTaxRate()
Business logic to calculate discount percentage from API items.

#### Add updateOrderTotals() - After calculateDiscountPercent()
Orchestrates updating all order_total entries by calling db.UpdateOrderTotal() 5 times.

### 3. **No new entity file needed**
The `OrderProductData` struct is defined in the database package since it's tightly coupled to database operations.

## Design Decisions (Resolved)

1. **Tax rate source**: ✅ Calculate from existing order_total (sub_total and tax)

2. **Discount calculation**: ✅ Calculate percentage from API item totals vs full totals:
   - `discountPercent = 1 - sum(ApiOrderedItem.Total) / sum(Price × Quantity)`
   - Apply to order: `discount = (subTotal + tax + shipping) × discountPercent`

3. **Shipping**: ✅ Preserve existing shipping from order_total table

4. **Item total**: ✅ Recalculate as `Price × Quantity`, ignore API item.Total

5. **Tax title**: ✅ Preserve existing titles from order_total table

6. **order_total updates**: Use `INSERT...ON DUPLICATE KEY UPDATE` for idempotency

## Database Schema Notes

The `order_total` table likely has a unique constraint on `(order_id, code)` which allows `INSERT...ON DUPLICATE KEY UPDATE` to work. If not, we may need to:
1. Use `DELETE FROM order_total WHERE order_id = ? AND code IN (...)` first
2. Then `INSERT` new rows

Check the schema or test the UPSERT approach first.

## Testing Considerations

- Test with orders that have existing totals in order_total table
- Test with different tax rates (23%, 0%, etc.)
- Test with multiple items (verify sum calculations)
- Test with shipping costs (verify preservation)
- Test with discounts of different percentages
- Test with edge case: no discount (discountPercent = 0)
- Test with edge case: 100% discount (discountPercent = 1.0)
- Verify money conversions (cents ↔ float) don't lose precision
- Check rounding errors don't accumulate across many items
- Verify calculated order total matches API grand_total (within rounding tolerance)
