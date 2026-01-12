package core

import (
	"fmt"
	"log/slog"
	"math"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

const (
	ZohoLocation    = "Польша"
	ZohoOrderSource = "OpenCart"

	ChunkSize = 100
)

type Currency struct {
	Code string
	Rate float64
}

// PushOrderToZoho fetches an order by ID from the database and pushes it to Zoho CRM.
// Returns the Zoho order ID on success.
func (c *Core) PushOrderToZoho(orderId int64) (string, error) {

	_, order, err := c.repo.OrderSearchId(orderId)
	if err != nil {
		return "", fmt.Errorf("order search: %w", err)
	}

	zohoId, err := c.processOrder(order, order.ClientDetails.IsB2B())
	if err != nil {
		return "", err
	}

	err = c.repo.ChangeOrderZohoId(orderId, zohoId)
	if err != nil {
		return zohoId, fmt.Errorf("update zoho_id in database: %w", err)
	}

	return zohoId, nil
}

// processOrder handles the core order-to-Zoho flow: creates contact, validates products,
// builds and creates the Zoho order with all items. Returns the Zoho order ID on success.
func (c *Core) processOrder(order *entity.CheckoutParams, isB2B bool) (string, error) {
	log := c.log.With(
		slog.Int64("order_id", order.OrderId),
		slog.String("currency", order.Currency),
		slog.String("tax", order.TaxTitle),
		slog.Float64("total", round2(order.Total)),
		slog.Float64("tax_value", round2(order.TaxValue)),
		slog.String("coupon", order.CouponTitle),
		slog.Float64("shipping", order.Shipping),
		slog.String("name", fmt.Sprintf("%s : %s", order.ClientDetails.FirstName, order.ClientDetails.LastName)),
		slog.String("country", order.ClientDetails.Country),
	)

	if err := order.Validate(); err != nil {
		log.With(sl.Err(err)).Warn("validation failed")
		return "", err
	}

	// Create or find contact in Zoho
	contactID, err := c.zoho.CreateContact(order.ClientDetails)
	if err != nil {
		log.With(
			slog.String("email", order.ClientDetails.Email),
			slog.String("phone", order.ClientDetails.Phone),
			sl.Err(err),
		).Error("create contact")
		return "", fmt.Errorf("create contact: %w", err)
	}

	// Validate product UIDs
	if err := hasEmptyUid(order.LineItems); err != nil {
		return "", fmt.Errorf("product without UID: %w", err)
	}

	// Fetch missing Zoho IDs for products
	if err := hasEmptyZohoID(order.LineItems); err != nil {
		c.processProductsWithoutZohoID(order.LineItems)

		if err := hasEmptyZohoID(order.LineItems); err != nil {
			return "", fmt.Errorf("product without Zoho ID: %w", err)
		}
	}

	// Build and create Zoho order

	zohoId := ""
	infoTag := "order created"
	if !isB2B {
		zohoOrder, chunkedItems := c.buildZohoOrder(order, contactID)
		zohoId, err = c.zoho.CreateOrder(zohoOrder)
		if err != nil {
			return "", fmt.Errorf("create Zoho order: %w", err)
		}

		// Add remaining items in chunks
		for i, chunk := range chunkedItems {
			_, err = c.zoho.AddItemsToOrder(zohoId, chunk)
			if err != nil {
				log.With(
					sl.Err(err),
					slog.Int("chunk", i+1),
				).Error("add items to order")
				return "", fmt.Errorf("add items to order (chunk %d): %w", i+1, err)
			}
		}
	} else {
		zohoOrder, chunkedItems := c.buildZohoOrderB2B(order, contactID)
		zohoId, err = c.zoho.CreateB2BOrder(zohoOrder)
		if err != nil {
			return "", fmt.Errorf("create Zoho order: %w", err)
		}

		// Add remaining items in chunks
		for i, chunk := range chunkedItems {
			_, err = c.zoho.AddItemsToOrderB2B(zohoId, chunk)
			if err != nil {
				log.With(
					sl.Err(err),
					slog.Int("chunk", i+1),
				).Error("add items to order")
				return "", fmt.Errorf("add items to order (chunk %d): %w", i+1, err)
			}
		}

		infoTag = "B2B order created"
	}

	log.With(slog.String("zoho_id", zohoId)).Info(infoTag)

	return zohoId, nil
}

// ProcessOrders fetches all new orders from the database and pushes them to Zoho CRM.
// B2B orders are skipped and marked with "[B2B]" zoho_id. Orders with missing product
// UIDs or Zoho IDs are skipped until the missing data is available.
func (c *Core) ProcessOrders() {
	orders, err := c.repo.GetNewOrders()
	if err != nil {
		c.log.With(sl.Err(err)).Error("failed to get new orders")
		return
	}

	for _, order := range orders {
		log := c.log.With(
			slog.Int64("order_id", order.OrderId),
		)

		if order.ClientDetails.IsB2B() {
			log.With(slog.Int64("group_id", order.ClientDetails.GroupId)).Debug("b2b client")
			//_ = c.repo.ChangeOrderZohoId(order.OrderId, "[B2B]")
			//continue
		}

		zohoId, err := c.processOrder(order, order.ClientDetails.IsB2B())
		if err != nil {
			log.With(sl.Err(err)).Error("process order failed")
			continue
		}

		err = c.repo.ChangeOrderZohoId(order.OrderId, zohoId)
		if err != nil {
			log.With(sl.Err(err)).Error("update order zoho_id")
		}
	}
}

// hasEmptyZohoID checks if any product in the slice has an empty ZohoId.
// Returns an error with product details if found, nil otherwise.
func hasEmptyZohoID(products []*entity.LineItem) error {
	for _, p := range products {
		if p.ZohoId == "" {
			return fmt.Errorf("product id=%d %s has empty zoho_id", p.Id, p.Name)
		}
	}
	return nil
}

// hasEmptyUid checks if any product in the slice has an empty UID.
// Returns an error with product details if found, nil otherwise.
func hasEmptyUid(products []*entity.LineItem) error {
	for _, p := range products {
		if p.Uid == "" {
			return fmt.Errorf("product id=%d %s has empty UID", p.Id, p.Name)
		}
	}
	return nil
}

// processProductsWithoutZohoID fetches Zoho IDs from the product repository for products
// that don't have them. Updates both the in-memory slice and the database.
func (c *Core) processProductsWithoutZohoID(products []*entity.LineItem) {
	for i, p := range products {
		if p.ZohoId == "" {
			zohoID, err := c.prodRepo.GetProductZohoID(p.Uid)
			if err != nil {
				c.log.With(
					slog.String("product", p.Name),
					slog.String("product_uid", p.Uid),
					sl.Err(err),
				).Error("get product")
				continue
			}

			if zohoID != "" {
				err = c.repo.UpdateProductZohoId(p.Uid, zohoID)
				if err != nil {
					c.log.With(
						slog.String("product", p.Name),
						slog.String("product_uid", p.Uid),
						slog.String("zoho_id", zohoID),
						sl.Err(err),
					).Error("update product")
					continue
				}
				products[i].ZohoId = zohoID
			}
		}
	}
}

// buildOrderedItem converts a LineItem to a Zoho OrderedItem with the given discount percentage.
func buildOrderedItem(lineItem *entity.LineItem, discountP float64) entity.OrderedItem {
	totalWithDiscount := round2(lineItem.Qty * lineItem.Price * discountP / 100)
	return entity.OrderedItem{
		Product: entity.ZohoProduct{
			ID: lineItem.ZohoId,
		},
		Quantity: int64(lineItem.Qty),
		//Discount:
		DiscountP: discountP,
		ListPrice: lineItem.Price,
		Total:     totalWithDiscount,
	}
}

func buildGood(lineItem *entity.LineItem, currency Currency, discountP float64) entity.Good {
	totalWithDiscount := round2(lineItem.Qty * lineItem.Price * discountP / 100)
	good := entity.Good{
		Product: entity.ZohoProduct{
			ID: lineItem.ZohoId,
		},
		Quantity: int64(lineItem.Qty),
		//Discount:
		DiscountP: discountP,
	}

	switch currency.Code {
	case entity.CurrencyUAH:
		good.PriceUAH = round2(lineItem.Price * currency.Rate)
		good.TotalUAH = round2(totalWithDiscount * currency.Rate)
		break
	case entity.CurrencyPLN:
		good.PricePLN = lineItem.Price
		good.PricePLN = totalWithDiscount
		break
	case entity.CurrencyUSD:
		good.PriceUSD = round2(lineItem.Price * currency.Rate)
		good.TotalUSD = round2(totalWithDiscount * currency.Rate)
		break
	case entity.CurrencyEUR:
		good.PriceEUR = round2(lineItem.Price * currency.Rate)
		good.TotalEUR = round2(totalWithDiscount * currency.Rate)
		break
	}

	return good
}

// buildZohoOrder constructs a ZohoOrder from CheckoutParams. Returns the order and any
// additional item chunks that exceed ChunkSize (100 items) for subsequent API calls.
func (c *Core) buildZohoOrder(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrder, [][]*entity.OrderedItem) {
	discount, discountP := oc.GetDiscount()
	discountP = round0(discountP)

	lineItems := oc.LineItems

	// Build all ordered items
	allItems := make([]entity.OrderedItem, 0, len(lineItems))
	for _, d := range lineItems {
		allItems = append(allItems, buildOrderedItem(d, discountP))
	}
	// Add shipping as item without discount
	if oc.Shipping > 0 {
		shippingItem := &entity.LineItem{
			ZohoId: c.shippingItemZohoId,
			Qty:    1,
			Price:  oc.Shipping,
		}
		allItems = append(allItems, buildOrderedItem(shippingItem, 0))
	}

	// Split into main order items and chunks
	var orderedItems []entity.OrderedItem
	var chunkedItems [][]*entity.OrderedItem

	if len(allItems) <= ChunkSize {
		orderedItems = allItems
	} else {
		orderedItems = allItems[:ChunkSize]
		remaining := allItems[ChunkSize:]

		for i := 0; i < len(remaining); i += ChunkSize {
			end := i + ChunkSize
			if end > len(remaining) {
				end = len(remaining)
			}
			chunk := make([]*entity.OrderedItem, end-i)
			for j := i; j < end; j++ {
				chunk[j-i] = &remaining[j]
			}
			chunkedItems = append(chunkedItems, chunk)
		}
	}

	// if an order has coupon set, move discount percent to promocode
	couponTitle := ""
	couponValue := 0.0
	if oc.CouponTitle != "" {
		couponTitle = oc.CouponTitle
		couponValue = discount
		discount = 0
		discountP = 0.0
	}

	return entity.ZohoOrder{
		ContactName:        entity.ContactName{ID: contactID},
		OrderedItems:       orderedItems,
		Discount:           round2(discount),
		DiscountP:          round0(discountP),
		CouponTitle:        couponTitle,
		CouponValue:        round2(couponValue),
		Description:        oc.Comment,
		CustomerNo:         "",
		ShippingState:      "",
		Tax:                0,
		VAT:                round0(oc.TaxRate()),
		GrandTotal:         round2(oc.Total),
		SubTotal:           round2(oc.Total - oc.TaxValue),
		Currency:           oc.Currency,
		BillingCountry:     oc.ClientDetails.Country,
		Carrier:            "",
		Status:             c.statuses[oc.StatusId],
		SalesCommission:    0,
		DueDate:            time.Now().Format("2006-01-02"),
		BillingStreet:      oc.ClientDetails.Street,
		Adjustment:         0,
		TermsAndConditions: "Standard terms apply.",
		BillingCode:        oc.ClientDetails.ZipCode,
		ProductDetails:     nil,
		Subject:            fmt.Sprintf("Order #%d", oc.OrderId),
		IDsite:             fmt.Sprintf("%d", oc.OrderId),
		Location:           ZohoLocation,
		OrderSource:        ZohoOrderSource,
	}, chunkedItems
}

func (c *Core) buildZohoOrderB2B(oc *entity.CheckoutParams, contactID string) (entity.ZohoOrderB2B, [][]*entity.Good) {
	_, discountP := oc.GetDiscount()
	discountP = round0(discountP)

	lineItems := oc.LineItems

	orderCurrency := Currency{
		Code: oc.Currency,
		Rate: oc.CurrencyValue,
	}

	// Build all ordered items
	allItems := make([]entity.Good, 0, len(lineItems))
	for _, d := range lineItems {
		allItems = append(allItems, buildGood(d, orderCurrency, discountP))
	}
	// Add shipping as item without discount
	if oc.Shipping > 0 {
		shippingItem := &entity.LineItem{
			ZohoId: c.shippingItemZohoId,
			Qty:    1,
			Price:  oc.Shipping,
		}
		allItems = append(allItems, buildGood(shippingItem, orderCurrency, 0))
	}

	// Split into main order items and chunks
	var orderedItems []entity.Good
	var chunkedItems [][]*entity.Good

	if len(allItems) <= ChunkSize {
		orderedItems = allItems
	} else {
		orderedItems = allItems[:ChunkSize]
		remaining := allItems[ChunkSize:]

		for i := 0; i < len(remaining); i += ChunkSize {
			end := i + ChunkSize
			if end > len(remaining) {
				end = len(remaining)
			}
			chunk := make([]*entity.Good, end-i)
			for j := i; j < end; j++ {
				chunk[j-i] = &remaining[j]
			}
			chunkedItems = append(chunkedItems, chunk)
		}
	}

	// if an order has coupon set, move discount percent to promocode
	//couponTitle := ""
	//couponValue := 0.0
	//if oc.CouponTitle != "" {
	//	couponTitle = oc.CouponTitle
	//	couponValue = discount
	//	discount = 0
	//	discountP = 0.0
	//}

	order := entity.ZohoOrderB2B{
		ContactName: entity.ContactName{ID: contactID},
		Goods:       orderedItems,
		//Discount:           round2(discount),
		DiscountP:   round0(discountP),
		Description: oc.Comment,
		//CustomerNo:         "",
		VAT:            round0(oc.TaxRate()),
		Currency:       oc.Currency,
		BillingCountry: oc.ClientDetails.Country,
		Status:         c.statusesB2B[oc.StatusId],
		Pipeline:       "B2B",
		BillingStreet:  oc.ClientDetails.Street,
		Subject:        fmt.Sprintf("Order #%d", oc.OrderId),
		Location:       ZohoLocation,
		OrderSource:    ZohoOrderSource,
	}

	switch orderCurrency.Code {
	case entity.CurrencyUAH:
		order.GrandTotalUAH = round2(oc.Total * orderCurrency.Rate)
		order.SubTotalUAH = round2((oc.Total - oc.TaxValue) * orderCurrency.Rate)
		break
	case entity.CurrencyPLN:
		order.GrandTotalPLN = round2(oc.Total)
		order.SubTotalPLN = round2(oc.Total - oc.TaxValue)
		break
	case entity.CurrencyUSD:
		order.GrandTotalUSD = round2(oc.Total * orderCurrency.Rate)
		order.SubTotalUSD = round2((oc.Total - oc.TaxValue) * orderCurrency.Rate)
		break
	case entity.CurrencyEUR:
		order.GrandTotalEUR = round2(oc.Total * orderCurrency.Rate)
		order.SubTotalEUR = round2((oc.Total - oc.TaxValue) * orderCurrency.Rate)
		break
	}

	return order, chunkedItems
}

// round0 rounds a float64 to the nearest integer, converting negative values to positive.
func round0(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value)
}

// round2 rounds a float64 to 2 decimal points
func round2(value float64) float64 {
	if value < 0 {
		value = -value
	}
	return math.Round(value*100) / 100
}
