package daemon

const (
	PriorityDisconnectINX = iota // no dependencies
	PriorityStopCoordinator
	PriorityStopTendermint
)
