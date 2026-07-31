# Discount backfill

Repairs Sales Orders synced while `buildZohoOrder` folded the shipping VAT into the product
lines. Those orders went to Zoho with an inflated per-line discount (e.g. 10.79 % instead of the
coupon's 10 %), which leaves Zoho's grand total short of what the customer was charged by
`Shipping × VAT rate` — about 8.38 zł on a 39.90 DHL order at 21 %.

It is a one-shot mode of the normal binary: it repairs the orders placed on one day and exits
without starting the poller or the HTTP server, so nothing can interleave with the rewrite.

## Running it

Always dry-run first — this is the default, `-apply` is what writes:

```bash
# report what would change
/usr/local/bin/zohoclient -conf=/etc/conf/config.yml -log=/var/log/ -backfill=2026-07-31

# write the corrections
/usr/local/bin/zohoclient -conf=/etc/conf/config.yml -log=/var/log/ -backfill=2026-07-31 -apply
```

The date is `YYYY-MM-DD` in the server's local time and selects orders by `oc_order.date_added`.
Re-running is safe: an order already carrying the right figures is reported as unchanged and no
API call is made for it.

## What it does per order

1. Loads every order placed that day whose `zoho_id` names a real Sales Order (`[B2B]` and empty
   are excluded).
2. Rebuilds the correct subform with the current `buildZohoOrder`, and GETs what Zoho holds.
3. Compares row by row, in subform order. Row count, product id and quantity must all match — if
   any of them differ the order was edited in Zoho and is **skipped**, never overwritten.
4. Rewrites only `List_Price` and `DiscountP` (and the line `Total`), and only on rows that
   actually drifted. The non-taxable carriage row is already correct and is left alone.
5. Records the resulting `Modified_Time` in `oc_order.zoho_modified_time`, so the webhook our own
   write provokes is recognised as an echo instead of being reverse-synced.

Rows are updated **in place by their Zoho subform row id**. This is not optional: a subform row
sent without its `id` is appended as a new line rather than updating anything, and an empty array
deletes every row
([Zoho: Update Subform Data](https://www.zoho.com/crm/developer/docs/api/v8/update-subforms.html)).
`ZohoService.UpdateOrderItemRows` refuses to send a row that has no id.

## Reading the report

The closing line counts `scanned`, `corrected`, `unchanged`, `skipped` and `failed`. Every
corrected order also logs `discount_was` / `discount_now` and `zoho_total_was` / `charged`, so the
drift is visible per order. Skipped orders log why they no longer match OpenCart — they need a
human, not a rerun.

The process exits non-zero if any order failed.
