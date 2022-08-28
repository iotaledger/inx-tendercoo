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
  - `COO_PRV_KEY` specifies the private Ed25519 _Milestone Key_ (according to [RFC 8032](https://datatracker.ietf.org/doc/html/rfc8032), i.e. any 32-byte string) for the current validator in hex-encoding. The key is used to sign the produced IOTA milestones.
- Config file:
  - `tendermint.bindAddress` specifies the bind address for incoming connections without a protocol prefix.
  - `tendermint.consensusPrivateKey` specifies the private Ed25519 _Consensus Key_ in hex-encoding. The key is used to participate in the Tendermint consensus.
  - `tenderming.nodePrivateKey` specifies the private Ed25519 Node Key_ in hex-encoding. The key is used to encrypt and sign Tendermint P2P communication.
  - `tendermint.root` specifies the root folder for Tendermint to store its config and keep its database.
  - `tendermint.logLevel` specifies the logging level of the Tendermint Core in ASCII. It cannot be lower than the global log level (e.g. a Tendermint log level of `DEBUG` does not add more verbosity when the global level is `INFO`).
  - `tendermint.genesisTime` specifies the time the Tendermint blockchain started or will start in Unix time using seconds. If validators are started before this time, they will sit idle until the time specified.
  - `tendermint.chainID` specifies the identifier of the Tendermint blockchain. The ChainID must match the network ID configured in the node.
  - `tendermint.peers` specifies the list of Tendermint nodes to connect to. As each plugin is usually run as one validator, this list should contain the addresses of all the validators. Each address is specified as `ID@host:port`. Here `ID` denotes the _Node ID_ of the corresponding Tendermint node, which corresponds to the hex-encoded first 20 bytes of the SHA-256 hash of the public _Node Key_. For example:
    - The private Ed25519 key `0fe83b7d5d6551904c3eb7a770f3ddb5c063993db3576b97c17c0308f67f11ae`
    - corresponds to the ID `344713fc9f17906035de518e6efa4a2015c366bf`.
  - For each validator with `$NAME`:
    - `tendermint.validators.$NAME.pubKey` specifies the consensus key of the validator.
    - `tendermint.validators.$NAME.power` specifies the voting power of the validator.

Additional information on running Tendermint in production can be found here: [Tendermint Core / Overview / Running in production](https://docs.tendermint.com/v0.34/tendermint-core/running-in-production.html)

## Tip Selection

Each validator proposes one tip to the consensus mechanism for selection as a milestone parent. This process is called _Tip Selection_ and is based on the following algorithm:

### Heaviest Tip Heuristic

#### Global
- $B$ &mdash; considered blocks
- $R_j\ \forall j \in B$ &mdash; blocks referenced by block $j$
- $T$ &mdash; tip set


#### $\texttt{OnBlockSolid}(b)$

Input:
- $b$ &mdash; newly solid and valid block
 
Steps:
- $B \gets B \cup \{b\}$
- compute and store the set of referenced blocks $R_b \gets \{b\} \cup \bigcup_{j \in \operatorname{parents}(b)\cap B} R_j$
- update the tip set $T \gets (T \cup \{b\}) \setminus \operatorname{parents}(b)$


#### $\texttt{Select}()$

Output:
- $S$ &mdash; subset of selected tips

Steps:

- $S \gets \emptyset$
- while $|S| <$ `MaxTips`
  - $t \gets \operatorname{arg\,max}_{j \in T} |R_j|$
  - if $\frac{|R_t|}{\operatorname{arg\,max}_{j \in S} |R_j|}$ < `ReducedConfirmationLimit`:
    - break
  - For each $j \in B$:
    - $R_j \gets R_j \setminus R_t$
  - $S \gets S \cup \{t\}$ 
- reset tracked blocks $B \gets \emptyset$ and all $R_j$
- return $S$ 

#### Implementation Details

- Each block $b \in B$ is assigned a unique integer index.
- The sets $R_j$ are represented as bit vectors, with the bit $R_j[i]$ denoting whether the block with index $i$ is contained in the set.
- While bit vectors allow for very efficient computations of union and difference, they require a lot of space: $\mathcal{O}(|B|^2)$. To prevent this from getting out of hand, the set $B$ needs to be reset after each $\texttt{Select}$. (With this step we effectively lose track of all the blocks which are not referenced by $S$. However, this becomes less relevant the more blocks are referenced with one $\texttt{Select}$.)
- To further limit the size of $B$, the creation of the next milestone must be prematurely triggered (and thus also a call of $\texttt{Select}$) when $B >$ `MaxTrackedBlocks`
- Note: $\texttt{Select}()$ chooses the tips using a greedy heuristic. In general, there can be another set of tips $S'$ of the same size that reference more blocks than $S$.

#### Parameters:
- `MaxTrackedBlocks` specifies the maximum number of blocks tracked by the milestone tip selection.
- `MaxTips` specifies the maximum number of tips returned by the tip selection.
- `ReducedConfirmationLimit`: Stop the selection, when tips reference less additional blocks than this fraction (compared to the number of tips referenced by the first and best tip).
