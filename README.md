# inx-tendercoo

## Bootstrapping

In order to bootstrap a network without any previous INX-TenderCoo milestones, the `cooBootstrap` command line flag needs to be set.
Then, additional bootstrap parameters can be set used additional command line flags:
- `--cooStartIndex uint32` specifies the `Index Number` of the first issued milestone.
- `--cooStartMilestoneID byte32Hex` specifies the `Previous Milestone ID` of the first issued milestone in hex-encoding. According to TIP-29 this can only be all zero, if `Index Number` equals the `First Milestone Index` protocol parameter. Otherwise, it must reference the previous milestone.
- `--cooStartMilestoneBlockID byte32Hex` specifies the _Block ID_ of a block containing the milestone with ID matching `Previous Milestone ID`. If `Index Number` equals `First Milestone Index`, this can be all zero. Otherwise, it must reference the previous milestone.
