# INX-TenderCoo

This repository contains an [INX](https://github.com/iotaledger/inx) plugin for a distributed coordinator using [Tendermint Core](https://github.com/tendermint/tendermint) BFT consensus. Each plugin contains its own Tendermint node and represents one validator in the network.

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

Additional information on running Tendermint in production can be found here: [Tendermint Core / Overview / Running in production](https://docs.tendermint.com/v0.34/tendermint-core/running-in-production.html)

## Tip Selection

Each validator proposes one tip to the consensus mechanism for selection as a milestone parent. This process is called _Tip Selection_ and is based on the following algorithm:

### Heaviest Tip Heuristic

#### Global

- B &ndash; considered blocks
- Rⱼ ∀j∈B &ndash; blocks referenced by block j
- T &ndash; tip set
-
#### OnBlockSolid(𝑖)

Input:
- 𝑖 &ndash; newly solid and valid block

Steps:
- B←B∪{𝑖}
- compute and store the set of referenced blocks Rᵢ←{𝑖}∪⋃j∈parents(𝑖)∩BRⱼ
- update the tip set T←(T∪{𝑖})∖parents(𝑖)

#### Select()

Output:
- S &ndash; subset of selected tips

Steps:
- S←∅
- while |S|< `MaxTips`
  - t←argmaxj∈T|Rⱼ|
  - if |Rₜ|/argmaxj∈S|Rⱼ| < `ReducedConfirmationLimit`:
    - break
  - For each j∈B:
    - Rⱼ←Rⱼ∖Rₜ
  - S←S∪{t}
- reset tracked blocks B←∅ and all Rⱼ
- return S

#### Implementation Details

- Each block b∈B is assigned a unique integer index.
- The sets Rⱼ are represented as bit vectors, with the bit Rⱼ[i] denoting whether the block with index i is contained in the set.
- While bit vectors allow for very efficient computations of union and difference, they require a lot of space: O(|B|²). To prevent this from getting out of hand, the set B needs to be reset after each Select. (With this step we effectively lose track of all the blocks which are not referenced by S. However, this becomes less relevant the more blocks are referenced with one Select.)
- To further limit the size of B, the creation of the next milestone must be prematurely triggered (and thus also a call of Select) when B> MaxTrackedBlocks
- Note: Select() chooses the tips using a greedy heuristic. In general, there can be another set of tips S′ of the same size that reference more blocks than S.

#### Parameters:
- `MaxTrackedBlocks` specifies the maximum number of blocks tracked by the milestone tip selection.
- `MaxTips` specifies the maximum number of tips returned by the tip selection.
- `ReducedConfirmationLimit`: Stop the selection, when tips reference less additional blocks than this fraction (compared to the number of tips referenced by the first and best tip).
