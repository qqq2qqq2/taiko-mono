package indexer

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/taikoxyz/taiko-mono/packages/relayer"
	"github.com/taikoxyz/taiko-mono/packages/relayer/contracts/bridge"
)

// handleEvent handles an individual MessageSent event
func (svc *Service) handleEvent(
	ctx context.Context,
	chainID *big.Int,
	event *bridge.BridgeMessageSent,
) error {
	raw := event.Raw

	log.Infof("event found for msgHash: %v", common.Hash(event.MsgHash).Hex())

	// handle chain re-org by checking Removed property, no need to
	// return error, just continue and do not process.
	if raw.Removed {
		log.Warnf("event msgHash was removed: %v", common.Hash(event.MsgHash).Hex())
		return nil
	}

	if event.MsgHash == relayer.ZeroHash {
		log.Warn("Zero msgHash found. This is unexpected. Returning early")
		return nil
	}

	eventStatus, err := svc.eventStatusFromMsgHash(ctx, event.Message.GasLimit, event.MsgHash)
	if err != nil {
		return errors.Wrap(err, "svc.eventStatusFromMsgHash")
	}

	marshaled, err := json.Marshal(event)
	if err != nil {
		return errors.Wrap(err, "json.Marshal(event)")
	}

	e, err := svc.eventRepo.Save(ctx, relayer.SaveEventOpts{
		Name:    eventName,
		Data:    string(marshaled),
		ChainID: chainID,
		Status:  eventStatus,
	})
	if err != nil {
		return errors.Wrap(err, "svc.eventRepo.Save")
	}

	if !canProcessMessage(ctx, eventStatus, event.Message.Owner, svc.relayerAddr) {
		log.Warnf("cant process msgHash: %v, eventStatus: %v", common.Hash(event.MsgHash).Hex(), eventStatus)
		return nil
	}

	// process the message
	if err := svc.processor.ProcessMessage(ctx, event, e); err != nil {
		return errors.Wrap(err, "svc.processMessage")
	}

	return nil
}

func canProcessMessage(
	ctx context.Context,
	eventStatus relayer.EventStatus,
	messageOwner common.Address,
	relayerAddress common.Address,
) bool {
	// we can not process, exit early
	if eventStatus == relayer.EventStatusNewOnlyOwner {
		if messageOwner != relayerAddress {
			log.Infof("gasLimit == 0 and owner is not the current relayer key, can not process. continuing loop")
			return false
		}

		return true
	}

	if eventStatus == relayer.EventStatusNew {
		return true
	}

	return false
}

func (svc *Service) eventStatusFromMsgHash(
	ctx context.Context,
	gasLimit *big.Int,
	signal [32]byte,
) (relayer.EventStatus, error) {
	var eventStatus relayer.EventStatus

	messageStatus, err := svc.destBridge.GetMessageStatus(nil, signal)
	if err != nil {
		return 0, errors.Wrap(err, "svc.destBridge.GetMessageStatus")
	}

	eventStatus = relayer.EventStatus(messageStatus)
	if eventStatus == relayer.EventStatusNew {
		if gasLimit == nil || gasLimit.Cmp(common.Big0) == 0 {
			// if gasLimit is 0, relayer can not process this.
			eventStatus = relayer.EventStatusNewOnlyOwner
		}
	}

	return eventStatus, nil
}
