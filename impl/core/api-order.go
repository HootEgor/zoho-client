package core

import (
	"fmt"
	"log/slog"
	"zohoclient/entity"
	"zohoclient/internal/lib/sl"
)

func (c *Core) UpdateOrder(orderDetails *entity.ApiOrder) error {
	// Validate input
	if orderDetails.ZohoID == "" {
		return fmt.Errorf("zoho_id is required")
	}

	log := c.log.With(
		slog.String("zoho_id", orderDetails.ZohoID),
	)

	// Find order by zoho_id
	orderId, order, err := c.repo.OrderSearchByZohoId(orderDetails.ZohoID)
	if err != nil {
		log.Error("failed to find order by zoho_id", sl.Err(err))
		return fmt.Errorf("order not found: %w", err)
	}

	log = log.With(slog.Int64("order_id", orderId))
	log.Info("updating order")

	// Update order status if provided
	if orderDetails.Status != "" {
		statusId := c.GetStatusIdByName(orderDetails.Status)
		if statusId == -1 {
			log.Warn("unknown status name, skipping status update",
				slog.String("status", orderDetails.Status))
		} else {
			err = c.repo.ChangeOrderStatus(orderId, int64(statusId), "Updated via API")
			if err != nil {
				log.Error("failed to update order status", sl.Err(err))
				return fmt.Errorf("failed to update status: %w", err)
			}
			log.Info("order status updated",
				slog.String("status", orderDetails.Status),
				slog.Int("status_id", statusId))
		}
	}

	// Update order items if provided
	if len(orderDetails.OrderedItems) > 0 {
		// Use grand_total from request if provided, otherwise keep existing order total
		orderTotal := orderDetails.GrandTotal
		if orderTotal == 0 {
			orderTotal = float64(order.Total) / 100.0 // Convert cents to float
		}

		err = c.repo.UpdateOrderItems(orderId, orderDetails.OrderedItems, order.CurrencyValue, orderTotal)
		if err != nil {
			log.Error("failed to update order items", sl.Err(err))
			return fmt.Errorf("failed to update items: %w", err)
		}
		log.Info("order items updated",
			slog.Int("items_count", len(orderDetails.OrderedItems)),
			slog.Float64("grand_total", orderTotal))
	}

	log.Info("order updated successfully",
		slog.Int64("order_id", orderId),
		slog.Int("status_id", order.StatusId))

	return nil
}
