# INX-TenderCoo

This repository contains an [INX](https://github.com/iotaledger/inx) plugin for a distributed coordinator using [Tendermint Core](https://github.com/tendermint/tendermint) BFT consensus.

## Bootstrapping

In order to bootstrap a network without any previous INX-TenderCoo milestones, the `--cooBootstrap` command line flag needs to be set.
Then, additional bootstrap parameters can be set used additional command line flags:
- `--cooStartIndex uint32` specifies the `Index Number` of the first issued milestone.
- `--cooStartMilestoneID byte32Hex` specifies the `Previous Milestone ID` of the first issued milestone in hex-encoding. According to TIP-29 this can only be all zero, if `Index Number` equals the `First Milestone Index` protocol parameter. Otherwise, it must reference the previous milestone.
- `--cooStartMilestoneBlockID byte32Hex` specifies the _Block ID_ of a block containing the milestone with ID matching `Previous Milestone ID`. If `Index Number` equals `First Milestone Index`, this can be all zero. Otherwise, it must reference the previous milestone.

The plugin performs some sanity checks whether the given `Index`, `Milestone ID` and `Milestone Block ID` is consistent with the latest milestone (if present) of the connected node.
It crashes when these checks fail. In an emergency, the bootstrapping fail-safes can be disabled completely by additionally setting the `--cooBootstrapForce` flag.

## Restart

Once bootstrapped, each issued milestone contains its state in the `Metadata` field and the Tendermint blockchain contains all information needed to recreate previous milestones.

When the INX-TenderCoo plugin (and thus Tendermint) is restarted after a stop, it will query the node for the milestone matching the latest milestone present in its blockchain. As long as this milestone is still available (not pruned), the plugin can reconstruct its local state at that time. After this, replayed Tendermint blocks by other validators will eventually lead to a consistent state among all validators.

This means that, as long as the pruning interval of the connected node is longer than the maximum downtime INX-TenderCoo plugin, it can always be restarted without issues.

## Config

- Environment variables:
  - `COO_PRV_KEY` specifies the Ed25519 private key (according to [RFC 8032](https://datatracker.ietf.org/doc/html/rfc8032), i.e. any 32-byte string) for the current validator in hex-encoding. This private key is used to sign the produced milestones.
- Config file:
  - `tendermint.root` specifies the root folder for Tendermint to store its config and keep its database.
  - `tendermint.logLevel` specifies the logging level of the Tendermint Core in ASCII. It cannot be lower than the global log level (e.g. a Tendermint log level of `DEBUG` does not add more verbosity when the global level is `INFO`).
  - `tendermint.genesisTime` specifies the time the Tendermint blockchain started or will start in Unix time using seconds. If validators are started before this time, they will sit idle until the time specified.
  - `tendermint.chainID` specifies the identifier of the Tendermint blockchain. Every chain must have a unique ID. The ChainID must be less than 50 characters.
  - For each validator with `$NAME`:
    - `tendermint.validators.$NAME.pubKey` specifies the consensus key of the validator.
    - `tendermint.validators.$NAME.power` specifies the voting power of the validator.
    - `tendermint.validators.$NAME.address` specifies the IP address and port of the validator.
