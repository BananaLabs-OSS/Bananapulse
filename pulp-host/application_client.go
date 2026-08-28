package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/BananaLabs-OSS/Pulp/run"
	"github.com/vmihailenco/msgpack/v5"
)

const orchestratorCell = "lua-orchestrator"
const orchestratorDispatchProvider = "orchestrator.dispatch"

const (
	eventMonitorCommand               = "bananapulse.monitor.command.v1"
	eventMonitorAdminCommand          = "bananapulse.monitor.admin.command.v1"
	eventMonitorMigrationImport       = "bananapulse.monitor.migration.import.v1"
	eventMonitorIngestAuthenticated   = "bananapulse.monitor.ingest.authenticated.v1"
	eventMonitorSweep                 = "bananapulse.monitor.sweep.v1"
	eventMonitorQuery                 = "bananapulse.monitor.query.v1"
	eventMonitorProjection            = "bananapulse.monitor.projection.v1"
	eventSubscriberSubscribe          = "bananapulse.subscriber.subscribe.v1"
	eventSubscriberConfirm            = "bananapulse.subscriber.confirm.v1"
	eventSubscriberUnsubscribe        = "bananapulse.subscriber.unsubscribe.v1"
	eventSubscriberProjection         = "bananapulse.subscriber.projection.v1"
	eventSubscriberConfirmationResend = "bananapulse.subscriber.confirmation.resend.v1"
	eventSubscriberAdminList          = "bananapulse.subscriber.admin.list.v1"
	eventSubscriberAdminGet           = "bananapulse.subscriber.admin.get.v1"
	eventSubscriberAdminDelete        = "bananapulse.subscriber.admin.delete.v1"
	eventSubscriberAdminStateSet      = "bananapulse.subscriber.admin.state.set.v1"
	eventSubscriberMigrationImport    = "bananapulse.subscriber.migration.import.v1"
	eventIncidentPublish              = "bananapulse.incident.publish.v1"
	eventMaintenancePublish           = "bananapulse.maintenance.publish.v1"
	eventEmailOutboxClaim             = "bananapulse.host.email.outbox.claim.v1"
	eventEmailReceiptApply            = "bananapulse.host.email.outbox.receipt.apply.v1"
)

type applicationClient struct {
	access run.ApplicationProviderAccess
}

type dispatchRequest struct {
	Event   string         `msgpack:"event"`
	Payload map[string]any `msgpack:"payload,omitempty"`
}

type dispatchResult struct {
	Value any `msgpack:"value,omitempty"`
}

func newApplicationClient(access run.ApplicationProviderAccess) (*applicationClient, error) {
	if access == nil {
		return nil, errors.New("application provider access is required")
	}
	return &applicationClient{access: access}, nil
}

func (c *applicationClient) dispatch(ctx context.Context, event string, payload map[string]any) (any, error) {
	if event == "" {
		return nil, errors.New("event is required")
	}
	wire, err := msgpack.Marshal(dispatchRequest{Event: event, Payload: payload})
	if err != nil {
		return nil, fmt.Errorf("encode %s dispatch: %w", event, err)
	}
	raw, err := c.access.CallProvider(ctx, orchestratorCell, orchestratorDispatchProvider, wire)
	if err != nil {
		return nil, fmt.Errorf("dispatch %s: %w", event, err)
	}
	var result dispatchResult
	if err := msgpack.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode %s dispatch: %w", event, err)
	}
	return result.Value, nil
}

func (c *applicationClient) callRaw(ctx context.Context, event string, request any, response any) error {
	var requestWire []byte
	var err error
	if request != nil {
		requestWire, err = msgpack.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", event, err)
		}
	}
	payload := map[string]any{}
	if requestWire != nil {
		payload["request_msgpack"] = string(requestWire)
	}
	value, err := c.dispatch(ctx, event, payload)
	if err != nil {
		return err
	}
	responseWire, err := resultBytes(value, "response_msgpack")
	if err != nil {
		return fmt.Errorf("%s response: %w", event, err)
	}
	if response == nil {
		return nil
	}
	if err := msgpack.Unmarshal(responseWire, response); err != nil {
		return fmt.Errorf("decode %s response: %w", event, err)
	}
	return nil
}

func (c *applicationClient) callProviderRaw(
	ctx context.Context,
	cell string,
	provider string,
	request any,
	response any,
) error {
	if cell == "" || provider == "" {
		return errors.New("cell and provider are required")
	}
	var requestWire []byte
	var err error
	if request != nil {
		requestWire, err = msgpack.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", provider, err)
		}
	}
	responseWire, err := c.access.CallProvider(ctx, cell, provider, requestWire)
	if err != nil {
		return fmt.Errorf("call %s/%s: %w", cell, provider, err)
	}
	if response == nil {
		return nil
	}
	if err := msgpack.Unmarshal(responseWire, response); err != nil {
		return fmt.Errorf("decode %s response: %w", provider, err)
	}
	return nil
}

func (c *applicationClient) publish(
	ctx context.Context,
	event string,
	monitorRequest any,
	notificationRequest any,
	monitorResponse any,
	notificationResponse any,
) error {
	monitorWire, err := msgpack.Marshal(monitorRequest)
	if err != nil {
		return fmt.Errorf("encode monitor request: %w", err)
	}
	notificationWire, err := msgpack.Marshal(notificationRequest)
	if err != nil {
		return fmt.Errorf("encode notification request: %w", err)
	}
	value, err := c.dispatch(ctx, event, map[string]any{
		"monitor_request_msgpack":      string(monitorWire),
		"notification_request_msgpack": string(notificationWire),
	})
	if err != nil {
		return err
	}
	monitorResult, err := resultBytes(value, "monitor_result_msgpack")
	if err != nil {
		return fmt.Errorf("%s monitor response: %w", event, err)
	}
	notificationResult, err := resultBytes(value, "notification_result_msgpack")
	if err != nil {
		return fmt.Errorf("%s notification response: %w", event, err)
	}
	if monitorResponse != nil {
		if err := msgpack.Unmarshal(monitorResult, monitorResponse); err != nil {
			return fmt.Errorf("decode monitor response: %w", err)
		}
	}
	if notificationResponse != nil {
		if err := msgpack.Unmarshal(notificationResult, notificationResponse); err != nil {
			return fmt.Errorf("decode notification response: %w", err)
		}
	}
	return nil
}

func resultBytes(value any, field string) ([]byte, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("result is %T, want object", value)
	}
	raw, exists := object[field]
	if !exists {
		return nil, fmt.Errorf("result is missing %q", field)
	}
	switch typed := raw.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return typed, nil
	default:
		return nil, fmt.Errorf("%q is %T, want MessagePack bytes", field, raw)
	}
}
