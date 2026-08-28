//go:build wasip1

package main

import (
	"context"
	"fmt"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	"github.com/vmihailenco/msgpack/v5"
)

func main() {}

func init() { pulp.OnInit(bootstrap) }

func bootstrap(_ []byte) error {
	store, err := openPulpStore()
	if err != nil {
		return fmt.Errorf("open subscriber store: %w", err)
	}
	owner, err := OpenOwner(store)
	if err != nil {
		return err
	}
	pulp.Provide(FnCatalog, func([]byte) ([]byte, error) {
		return msgpack.Marshal(Catalog{
			Version: ContractVersion,
			Commands: []string{
				FnSubscribe, FnConfirm, FnUnsubscribe, FnConfirmationResend,
				FnNotifyIncident, FnNotifyMaintenance, FnAdminDelete,
				FnAdminStateSet, FnMigrationImport, FnOutboxReceiptApply,
				FnDeliveryConfigSet, FnTransitionApply,
			},
			Queries:   []string{FnProjection, FnAdminList, FnAdminGet, FnOutboxClaim},
			OutboxABI: "subscription-outbox.outbox/v1",
		})
	})
	pulp.Provide(FnSubscribe, provide(func(v SubscribeRequest) (CommandResult, error) { return owner.Subscribe(context.Background(), v) }))
	pulp.Provide(FnConfirm, provide(func(v ConfirmRequest) (CommandResult, error) { return owner.Confirm(context.Background(), v) }))
	pulp.Provide(FnUnsubscribe, provide(func(v UnsubscribeRequest) (CommandResult, error) { return owner.Unsubscribe(context.Background(), v) }))
	pulp.Provide(FnConfirmationResend, provide(func(v ConfirmationResendRequest) (ConfirmationResendResult, error) {
		return owner.ResendConfirmation(context.Background(), v)
	}))
	pulp.Provide(FnNotifyIncident, provide(func(v NotificationRequest) (CommandResult, error) {
		return owner.NotifyIncident(context.Background(), v)
	}))
	pulp.Provide(FnNotifyMaintenance, provide(func(v NotificationRequest) (CommandResult, error) {
		return owner.NotifyMaintenance(context.Background(), v)
	}))
	pulp.Provide(FnProjection, func([]byte) ([]byte, error) {
		projection, err := owner.Projection(context.Background())
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(projection)
	})
	pulp.Provide(FnAdminList, provide(func(v AdminListRequest) (AdminSubscriberList, error) {
		return owner.AdminList(context.Background(), v)
	}))
	pulp.Provide(FnAdminGet, provide(func(v AdminGetRequest) (AdminSubscriberGet, error) {
		return owner.AdminGet(context.Background(), v)
	}))
	pulp.Provide(FnAdminDelete, provide(func(v AdminDeleteRequest) (AdminMutationResult, error) {
		return owner.AdminDelete(context.Background(), v)
	}))
	pulp.Provide(FnAdminStateSet, provide(func(v AdminStateSetRequest) (AdminMutationResult, error) {
		return owner.AdminStateSet(context.Background(), v)
	}))
	pulp.Provide(FnMigrationImport, provide(func(v MigrationImportRequest) (MigrationImportReceipt, error) {
		return owner.ImportLegacy(context.Background(), v)
	}))
	pulp.Provide(FnDeliveryConfigSet, provide(func(v DeliveryConfigSetRequest) (DeliveryConfigSetResult, error) {
		return owner.SetDeliveryConfig(context.Background(), v)
	}))
	pulp.Provide(FnTransitionApply, func(raw []byte) ([]byte, error) {
		result, err := owner.ApplyTransitionRaw(context.Background(), raw)
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(result)
	})
	pulp.Provide(FnOutboxClaim, provide(func(v OutboxClaimRequest) (OutboxClaim, error) { return owner.ClaimOutbox(context.Background(), v) }))
	pulp.Provide(FnOutboxReceiptApply, provide(func(v OutboxReceipt) (ReceiptApplyResult, error) { return owner.ApplyReceipt(context.Background(), v) }))
	return nil
}

func provide[I any, O any](handler func(I) (O, error)) pulp.Provider {
	return func(raw []byte) ([]byte, error) {
		var input I
		if err := msgpack.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		output, err := handler(input)
		if err != nil {
			return nil, err
		}
		return msgpack.Marshal(output)
	}
}
