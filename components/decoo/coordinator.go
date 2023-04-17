package decoo

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	tmconfig "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/privval"

	"github.com/iotaledger/hive.go/runtime/timeutil"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	iotago "github.com/iotaledger/iota.go/v3"
)

// initCoordinatorState initializes the coordinator state corresponding to the Tendermint app state.
func initCoordinatorState(ctx context.Context, conf *tmconfig.Config) error {
	if bootstrap {
		// assure that the startMilestoneBlockID is solid before bootstrapping the coordinator
		if err := waitUntilBlockSolid(ctx, startMilestoneBlockID); err != nil {
			return err
		}

		if err := deps.DeCoo.Bootstrap(bootstrapForce, startIndex, startMilestoneID, startMilestoneBlockID); err != nil {
			log.Warnf("fail-safe prevented bootstrapping with these parameters. If you know what you are doing, "+
				"you can additionally use the %s flag to disable any fail-safes.", strconv.Quote(CfgCoordinatorBootstrapForce))

			return fmt.Errorf("bootstrap failed: %w", err)
		}

		return nil
	}

	// for the coordinator the Tendermint app state corresponds to the Tangle
	// when the plugin was stopped but the corresponding node was running, this can lead to a situation where the latest
	// milestone is newer than the core Tendermint state of the plugin
	// as this is illegal in the Tendermint API, we retrieve the block height from the Tendermint core and try to find
	// the corresponding milestone to use for the coordinator app state
	pv := privval.LoadFilePV(conf.PrivValidatorKeyFile(), conf.PrivValidatorStateFile())
	// in the worst case, the height can be off by one, as the state files gets written before the DB
	// as it is hard to detect this particular case, we always subtract one to be on the safe side
	tendermintHeight := pv.LastSignState.Height - 1
	// this should not happen during resuming, as Tendermint needs to sign at least two blocks to produce a milestone
	if tendermintHeight < 0 {
		return errors.New("resume failed: no Tendermint state available")
	}

	// make sure the node is connected to at least one other peer
	// otherwise the note status may not reflect the network status
	if !Parameters.SingleNode {
		if err := waitUntilConnected(ctx); err != nil {
			return err
		}
	}

	// creating a new Tendermint node, starts the replay of blocks
	// the first thing Tendermint will do is call the ABCI Info method to query the Tendermint application state
	// for the DeCoo this application state corresponds to the Tangle, i.e. the newest confirmed milestone
	// for this information to be accurate, the node should be synced
	for {
		lmi, cmi := deps.MilestoneIndexFunc()
		if lmi > 0 && lmi == cmi {
			log.Infof("Node appears to be synced; latest=%d confirmed=%d", lmi, cmi)

			break
		}
		log.Warnf("node is not synced; retrying in %s", SyncRetryInterval)
		if !timeutil.Sleep(ctx, SyncRetryInterval) {
			return ctx.Err()
		}
	}

	// next, we need to determine the newest milestone in the Tangle that is compatible with the Tendermint block height
	// if the plugin was stopped for some time, the Tangle might be ahead of Tendermint blockchain
	// we need to find a milestone that is contained in the Tangle as well as the Tendermint blockchain

	// start with the confirmed milestone and walk down the milestone chain until we find one that is in the blockchain
	ms, err := deps.NodeBridge.ConfirmedMilestone()
	if err != nil {
		return fmt.Errorf("failed to retrieve latest milestone: %w", err)
	}
	if ms == nil {
		return errors.New("failed to retrieve latest milestone: no milestone available")
	}
	for {
		state, err := decoo.NewStateFromMilestone(ms.Milestone)
		if err != nil {
			return fmt.Errorf("milestone %d contains invalid metadata: %w", ms.Milestone.Index, err)
		}
		if tendermintHeight >= state.MilestoneHeight {
			break
		}

		// try the previous milestone
		ms, err = inxGetMilestone(ctx, state.MilestoneIndex-1)
		if err != nil {
			return fmt.Errorf("milestone %d cannot be retrieved: %w", state.MilestoneIndex-1, err)
		}
	}

	if err := deps.DeCoo.InitState(ms.Milestone); err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}

	return nil
}

func waitUntilBlockSolid(ctx context.Context, blockID iotago.BlockID) error {
	solidEventListener, err := inxRegisterBlockSolidEvent(ctx, blockID)
	if err != nil {
		return fmt.Errorf("failed to register block solid event: %w", err)
	}
	defer solidEventListener.Deregister()

	log.Infof("waiting for block %s to become solid", blockID)

	return solidEventListener.Wait(ctx)
}

func waitUntilConnected(ctx context.Context) error {
	for {
		peers, err := inxGetPeers(ctx)
		if err != nil {
			return fmt.Errorf("failed to get peers: %w", err)
		}
		// check for at least one connected peer
		for _, peer := range peers {
			if peer.Connected {
				log.Info("Node appears to be connected")

				return nil
			}
		}

		log.Warnf("node has no connected peers; retrying in %s", SyncRetryInterval)
		if !timeutil.Sleep(ctx, SyncRetryInterval) {
			return ctx.Err()
		}
	}
}
