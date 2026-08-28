package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

const (
	subscriberOwnerCell              = "subscription-outbox"
	subscriberDeliveryConfigProvider = "subscription-outbox.v1.delivery.config.set"
)

type subscriberDeliveryConfigRequest struct {
	Version            string `msgpack:"version" json:"version"`
	RequestID          string `msgpack:"request_id" json:"request_id"`
	UnsubscribeBaseURL string `msgpack:"unsubscribe_base_url" json:"unsubscribe_base_url"`
}

type subscriberDeliveryConfigResult struct {
	Version            string `msgpack:"version" json:"version"`
	Applied            bool   `msgpack:"applied" json:"applied"`
	UnsubscribeBaseURL string `msgpack:"unsubscribe_base_url" json:"unsubscribe_base_url"`
}

func configureSubscriberDelivery(
	ctx context.Context,
	client *applicationClient,
	unsubscribeBaseURL string,
) error {
	sum := sha256.Sum256([]byte(unsubscribeBaseURL))
	request := subscriberDeliveryConfigRequest{
		Version:            subscriberContractVersion,
		RequestID:          "bananapulse/delivery-config/v1/" + hex.EncodeToString(sum[:]),
		UnsubscribeBaseURL: unsubscribeBaseURL,
	}
	var result subscriberDeliveryConfigResult
	return client.callProviderRaw(
		ctx,
		subscriberOwnerCell,
		subscriberDeliveryConfigProvider,
		request,
		&result,
	)
}
