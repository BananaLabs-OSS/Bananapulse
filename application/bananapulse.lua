-- Bananapulse is the application; monitor and subscriber cells are reusable
-- state owners. Lua owns cross-owner sequencing but has no host privileges.
local MONITOR = "pulp-monitor"
local SUBSCRIBERS = "subscription-outbox"

local providers = {
  monitor_command = "monitor.v1.command",
  monitor_query = "monitor.v1.query",
  monitor_projection = "monitor.v1.projection",
  subscriber_subscribe = "subscription-outbox.v1.subscribe",
  subscriber_confirm = "subscription-outbox.v1.confirm",
  subscriber_unsubscribe = "subscription-outbox.v1.unsubscribe",
  subscriber_confirmation_resend = "subscription-outbox.v1.confirmation.resend",
  subscriber_projection = "subscription-outbox.v1.projection",
  subscriber_admin_list = "subscription-outbox.v1.admin.list",
  subscriber_admin_get = "subscription-outbox.v1.admin.get",
  subscriber_admin_delete = "subscription-outbox.v1.admin.delete",
  subscriber_admin_state_set = "subscription-outbox.v1.admin.state.set",
  subscriber_migration_import = "subscription-outbox.v1.migration.import",
  transition_apply = "subscription-outbox.v1.transition.apply",
  outbox_claim = "subscription-outbox.v1.outbox.claim",
  outbox_receipt_apply = "subscription-outbox.v1.outbox.receipt.apply",
}

local function deny(message)
  error("bananapulse composition denied: " .. message)
end

local function require_payload(payload)
  if type(payload) ~= "table" then deny("payload must be a table") end
  return payload
end

local function require_wire(payload, field)
  require_payload(payload)
  local value = payload[field]
  if type(value) ~= "string" or value == "" then
    deny(field .. " must contain MessagePack bytes")
  end
  return value
end

local function optional_wire(payload)
  if payload == nil then return "" end
  require_payload(payload)
  local value = payload.request_msgpack
  if value == nil then return "" end
  if type(value) ~= "string" then deny("request_msgpack must contain MessagePack bytes") end
  return value
end

local function response(raw)
  return { response_msgpack = raw }
end

local function forward(cell, provider)
  return function(payload)
    return response(pulp.call_raw(cell, provider, require_wire(payload, "request_msgpack")))
  end
end

local function forward_optional(cell, provider)
  return function(payload)
    return response(pulp.call_raw(cell, provider, optional_wire(payload)))
  end
end

-- The monitor result is the cross-owner receipt. The subscriber owner accepts
-- those exact bytes, creates intents only for committed domain transitions,
-- and deduplicates by the monitor command identity. Import mode returns no
-- transitions and never enters this workflow.
local function commit_transitions(request_field)
  return function(payload)
    local monitor_result = pulp.call_raw(
      MONITOR,
      providers.monitor_command,
      require_wire(payload, request_field)
    )
    local transition_result = pulp.call_raw(
      SUBSCRIBERS,
      providers.transition_apply,
      monitor_result
    )
    return {
      response_msgpack = monitor_result,
      monitor_result_msgpack = monitor_result,
      transition_result_msgpack = transition_result,
      -- Retain the first-wave field until all host projections consume the
      -- explicitly named transition result.
      notification_result_msgpack = transition_result,
    }
  end
end

pulp.on("bananapulse.monitor.command.v1", forward(MONITOR, providers.monitor_command))
pulp.on("bananapulse.monitor.admin.command.v1", commit_transitions("request_msgpack"))
pulp.on("bananapulse.monitor.migration.import.v1", forward(MONITOR, providers.monitor_command))
pulp.on("bananapulse.monitor.ingest.authenticated.v1", commit_transitions("request_msgpack"))
pulp.on("bananapulse.monitor.sweep.v1", commit_transitions("request_msgpack"))
pulp.on("bananapulse.monitor.query.v1", forward(MONITOR, providers.monitor_query))
pulp.on("bananapulse.monitor.projection.v1", forward_optional(MONITOR, providers.monitor_projection))

pulp.on("bananapulse.subscriber.subscribe.v1", forward(SUBSCRIBERS, providers.subscriber_subscribe))
pulp.on("bananapulse.subscriber.confirm.v1", forward(SUBSCRIBERS, providers.subscriber_confirm))
pulp.on("bananapulse.subscriber.unsubscribe.v1", forward(SUBSCRIBERS, providers.subscriber_unsubscribe))
pulp.on(
  "bananapulse.subscriber.confirmation.resend.v1",
  forward(SUBSCRIBERS, providers.subscriber_confirmation_resend)
)
pulp.on("bananapulse.subscriber.projection.v1", forward_optional(SUBSCRIBERS, providers.subscriber_projection))
pulp.on("bananapulse.subscriber.admin.list.v1", forward(SUBSCRIBERS, providers.subscriber_admin_list))
pulp.on("bananapulse.subscriber.admin.get.v1", forward(SUBSCRIBERS, providers.subscriber_admin_get))
pulp.on("bananapulse.subscriber.admin.delete.v1", forward(SUBSCRIBERS, providers.subscriber_admin_delete))
pulp.on(
  "bananapulse.subscriber.admin.state.set.v1",
  forward(SUBSCRIBERS, providers.subscriber_admin_state_set)
)
pulp.on(
  "bananapulse.subscriber.migration.import.v1",
  forward(SUBSCRIBERS, providers.subscriber_migration_import)
)

pulp.on("bananapulse.incident.publish.v1", commit_transitions("monitor_request_msgpack"))
pulp.on("bananapulse.maintenance.publish.v1", commit_transitions("monitor_request_msgpack"))

-- These events are deliberately host-only boundaries. Lua exposes the
-- private intent and durable receipt providers but never performs email I/O.
pulp.on("bananapulse.host.email.outbox.claim.v1", forward(SUBSCRIBERS, providers.outbox_claim))
pulp.on(
  "bananapulse.host.email.outbox.receipt.apply.v1",
  forward(SUBSCRIBERS, providers.outbox_receipt_apply)
)
