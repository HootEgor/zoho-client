# Configuration

The service is configured by one YAML file, passed with `-conf` (default `config.yml`).
`config.yml` in the repository root is the annotated template and the authoritative list of keys.

**One process serves one OpenCart shop.** A second shop runs a second process with its own copy of
this file, its own systemd unit and its own listen port — nothing in the code is multi-tenant. See
`zohoclient-config.yml` (shop 1) and `zohoclient-config-ua.yml` (shop 2, the UA site) for the production
files, whose `${PLACEHOLDER}` values the deploy workflows substitute.

## Sections

| Section | Purpose |
|---|---|
| `env` | Environment name used by the logger (`local`, `production`, …) |
| `sql` | OpenCart MySQL connection; `enabled: false` runs the service without a database |
| `mongo` | Optional order-version archive — **one database per instance**, see below |
| `telegram` | Optional admin notification bot — **one bot token per instance**, see below |
| `site` | **Everything that differs between shops** — see below |
| `zoho` | Zoho OAuth credentials plus the picklist vocabulary written onto records |
| `prod_repo` | External product repository used to fill in missing product Zoho IDs |
| `listen` | HTTP API bind address, port and bearer key |
| `smartsender` | Optional SmartSender ↔ Zoho Functions chat sync |

Two instances must not share a `site.log_file` either — they open it `O_APPEND` and would interleave
their output and share one rotation schedule.

Two instances must not share a `mongo.database`. Order versions are keyed by `order_id`
alone (`internal/database/mongo/mongo.go`, the `orders` collection), and OpenCart order ids restart
from 1 per shop, so a shared database would append versions of unrelated orders to one document.
Sharing the Mongo *server* is fine; give each shop its own database name.

Two instances must not share a `telegram.api_key`. Notifications (`sendMessage`) would work from
both, but the bot also long-polls `getUpdates` for the `/level` command, and Telegram allows only
one such connection per token — the instances would keep terminating each other's poll with
409 Conflict. Give each shop its own bot; `telegram.bot_name` then also tells their messages apart.

## The `site` section

Every key is optional. An omitted key falls back to the value the service used when these were Go
constants — the first shop's — so an older config file behaves exactly as it did before. The whole
section is resolved and validated once at startup by `config.SiteSettings()`
(`internal/config/site.go`); an invalid value stops the process there rather than surfacing on the
first order that syncs. The resolved settings are printed in the startup log
(`site settings resolved`), so a deployment can be checked against what the process actually
decided.

Defaults live in `config.DefaultSiteSettings()`, which is also what the tests build against.

| Key | Default | Meaning |
|---|---|---|
| `name` | `default` | Log tag identifying the shop (the `site=` field on every line) |
| `log_file` | `zohoclient.log` | Log file inside the `-log` directory; **must differ per instance** |
| `timezone` | `Europe/Warsaw` | OpenCart datetimes and the Zoho payment timestamp |
| `language_id` | `2` | `oc_product_description.language_id` to read product names from |
| `lookback_days` | `30` | How far back the order poller looks at `date_modified` |
| `batch_limit` | `10` | Max orders per poll of a single status |
| `poll_interval` | `120` | Order poll interval, seconds |
| `customer_poll_interval` | `300` | Customer sync interval, seconds |
| `nip_custom_field_id` | `"2"` | `oc_custom_field` id holding the buyer's tax number |
| `post_terminal_field` | `field29` | `oc_order_simple_fields` column with the parcel-locker code |
| `shipping_item_uid` | `cd3cc23c-…` | `oc_product.product_uid` carriage is billed as |
| `currencies` | `[PLN, EUR]` | Currencies an order may be placed in |
| `order_statuses.poll` | `[1,2,5,17,22,23]` | `order_status_id` values that trigger a sync |
| `order_statuses.new` | `1` | Fresh-order status; also the Zoho status fallback |
| `order_statuses.canceled` | `7` | |
| `b2b_group_ids` | `[6,16,18,19]` | Customer groups routed to Deals; `[]` means none |
| `customer_categories` | see template | `customer_group_id` → Zoho `customer_category` |
| `total_codes` | identity | logical total → `oc_order_total.code`; **merged**, list only what differs |
| `features.*` | all `true` | Optional subsystems, below |
| `shipping_code_map` | see template | shipping module code → logical post-type key |

### Feature flags

Each flag off makes its subsystem *inert*: the service never creates or reads the columns and
tables that subsystem owns, so a shop that lacks them runs cleanly.

| Flag | Off means |
|---|---|
| `features.payments` | `wf_payment_*` and `zoho_payment_*` columns are neither created nor selected; the payment pollers do not start; no Zoho Payments record is made. `zoho.payment_statuses` is then not required. |
| `features.customer_sync` | the customer poller does not start; `oc_customer.zoho_id` is not created. |
| `features.b2b` | no customer group counts as B2B, so no order is marked `[B2B]`; the `/zoho/webhook/b2b` route is not registered. |
| `smartsender.enabled` | the SmartSender poller and Zoho Functions client are not built. |

### Post types

Two hops, because both ends vary per shop while the rules in between do not:

```
shipping_method name  ──(keyword rules, pl/ua/en, in code)──┐
                                                            ├──▶ logical key ──▶ zoho.post_types ──▶ Post_type
site.shipping_code_map[shipping_code]  ─(fallback only)─────┘
```

The method **name** is matched first; `shipping_code` is only consulted when the name says nothing
recognisable, because production data holds rows where the code and the name disagree. The five
logical keys are `inpost`, `inpost_courier`, `inpost_terminal`, `dhl_courier`, `pickup`, and
`zoho.post_types` must define all of them.

## The `zoho` section

Beyond the OAuth credentials, this section carries the picklist values written onto Zoho records.
**Field API names are not configurable** — every shop syncs into the same Zoho org, so they stay
fixed in `entity/zoho-order.go`.

| Key | Default | Meaning |
|---|---|---|
| `location` | `Польша` | `Location_DR` |
| `order_source` | `OpenCart` | `Order_Source` |
| `terms` | `Standard terms apply.` | `Terms_and_Conditions` |
| `chunk_size` | `200` | Max subform rows per Sales Order API call |
| `b2b_pipeline` | `B2B` | Deals pipeline name |
| `order_status_map` | see template | `order_status_id` → Zoho `Status` name, **and its reverse** for inbound webhooks |
| `order_status_b2b_map` | see template | the same for the Deals pipeline |
| `post_types` | see template | logical post-type key → `Post_type` picklist |
| `payment_statuses` | see template | logical payment state → Payments status picklist |

`order_status_map` is used in **both** directions, and they are not symmetric:

- **Outbound**, a Sales Order the sync creates or overwrites always carries the name mapped for
  `order_statuses.new`, whatever the OpenCart status is. Zoho owns the record's status from there,
  so deriving it from OpenCart would let a re-push overwrite a status a Zoho user had moved on.
- **Inbound**, a status arriving on a Zoho webhook is resolved back to an OpenCart id through the
  whole map, so every status a Zoho user can set needs an entry here.

Because of that, do not trim the map to just the new-order status: that would leave inbound
webhooks unable to resolve any other status, and they would be logged as
`unknown status name from Zoho, keeping current`.

The Stripe/wfsync status strings that feed `payment_statuses` are a contract with wfsync, not a
per-site setting, and live in `entity/payment-status.go`.

## Validation

`SiteSettings()` rejects, at startup:

- an unloadable `timezone`
- a non-positive `language_id`, `lookback_days`, `batch_limit`, `poll_interval`,
  `customer_poll_interval` or `chunk_size`
- an empty `shipping_item_uid`, `currencies` or `order_statuses.poll`
- an unknown key in `total_codes`
- a `shipping_code_map` value that is not a key of `zoho.post_types`
- a `zoho.post_types` missing any of the five logical keys
- a `zoho.order_status_map` with no entry for `order_statuses.new`
- a `zoho.payment_statuses` missing any logical payment state, when `features.payments` is on
